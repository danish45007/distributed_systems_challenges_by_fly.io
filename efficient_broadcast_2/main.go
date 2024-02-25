package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/emirpasic/gods/trees/btree"
	maelstrom "github.com/jepsen-io/maelstrom/demo/go"
	log "github.com/sirupsen/logrus"
)

/*
Challenge:
	With the same node count of 25 and a message delay of 100ms, ensure that:
		Messages-per-operation is below 20
		Median latency is below 1 second
		Maximum latency is below 2 seconds

Solution:
	Batch all the messages in the defined batch duration and
	broadcast them to all the nodes in the network
	at once in a septate go routine.
	Also, use a flat tree to store the topology of the network for efficient discovery of target nodes.
	{
  		"type": "broadcast",
  		"messages": [1, 8, 72, 25]
	}
*/

const (
	MaxRetry       = 100                    // MaxRetries is the maximum number of retries in case of n/w failure
	BatchFrequency = 500 * time.Millisecond // BatchFrequency is the duration to batch the messages
)

type server struct {
	Node       *maelstrom.Node
	NodeId     string
	RootNodeId int

	// message
	messageIdsLock sync.RWMutex
	MessageIds     map[int]struct{}

	// topology
	topologyLock sync.RWMutex
	Topology     *btree.Tree // flat tree (root -> [c1,c2....]): efficient data structure to store the topology of the network

	// batch
	batchLock sync.RWMutex
	BatchMap  map[string][]int
}

func init() {
	f, err := os.OpenFile("/tmp/maelstrom.log", os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
	if err != nil {
		panic(err)
	}
	log.SetOutput(f)
}

func main() {
	newNode := maelstrom.NewNode()
	server := &server{Node: newNode, MessageIds: make(map[int]struct{}), BatchMap: make(map[string][]int)}

	newNode.Handle("init", server.initHandler)
	newNode.Handle("broadcast", server.broadcastHandler)
	newNode.Handle("read", server.readHandler)
	newNode.Handle("topology", server.topologyHandler)

	// broadcast the backlog of messages in a separate go routine
	// at the defined batch frequency
	go func() {
		for {
			select {
			case <-time.After(BatchFrequency):
				server.batchRPC()
			}
		}
	}()

	if err := newNode.Run(); err != nil {
		log.Fatal(err)
	}
}

func getRootId(nodeId string) (int, error) {
	// nodeId is the node id without the prefix "n"
	rootId, err := strconv.Atoi(nodeId[1:])
	if err != nil {
		return 0, err
	}
	return rootId, nil

}

// initHandler initializes the server
func (s *server) initHandler(_ maelstrom.Message) error {
	s.NodeId = s.Node.ID()
	// node with the lowest id is the root node
	// nodeId is the node id without the prefix "n"
	rootNodeId, err := getRootId(s.NodeId)
	if err != nil {
		return err
	}
	s.RootNodeId = rootNodeId
	return nil
}

func (s *server) broadcastHandler(msg maelstrom.Message) error {
	var body map[string]any
	// unmarshal the message body
	if err := json.Unmarshal(msg.Body, &body); err != nil {
		return err
	}

	// return back the message in a separate go routine
	go func() {
		_ = s.Node.Reply(msg, map[string]any{
			"type": "broadcast_ok",
		})
	}()
	// for the first message broadcast, the body will contain the single message
	// broadcast request coming from the upstream (underline process) to the server node
	if _, contains := body["message"]; contains {
		id := int(body["message"].(float64))
		s.messageIdsLock.Lock()
		// check if the message has been seen
		if _, exists := s.MessageIds[id]; exists {
			s.messageIdsLock.Unlock()
			return nil
		}
		// mark the message as seen
		s.MessageIds[id] = struct{}{}
		s.messageIdsLock.Unlock()

		// broadcast the message to all other nodes
		return s.broadcast(msg.Src, body)
	}
	// for the subsequent message broadcast, the body will contain the batch of messages
	// these broadcast requests are coming from the server node to the downstream nodes
	if _, contains := body["messages"]; contains {
		batchMessages := body["messages"].([]interface{})
		messages := make([]int, 0, len(batchMessages))
		s.messageIdsLock.Lock()
		for _, message := range batchMessages {
			messageId := int(message.(float64))
			// check if the message has been seen
			if _, exists := s.MessageIds[messageId]; exists {
				s.messageIdsLock.Unlock()
				continue
			}
			// mark the message as seen
			s.MessageIds[messageId] = struct{}{}
			messages = append(messages, messageId)
		}
		s.messageIdsLock.Unlock()
		return s.batchBroadcast(msg.Src, messages)
	}
	return nil
}

func (s *server) broadcast(src string, body map[string]any) error {
	s.topologyLock.RLock()
	// root node for the flat tree
	rootNode := s.Topology.GetNode(s.RootNodeId)
	defer s.topologyLock.RUnlock()

	var neighbors []string
	// get the neighbors of the root node
	if rootNode.Parent != nil {
		// append the parent node to the neighbors list
		neighbors = append(neighbors, rootNode.Parent.Entries[0].Value.(string))
	}

	// append the children nodes to the neighbors list
	for _, children := range rootNode.Children {
		for _, entry := range children.Entries {
			neighbors = append(neighbors, entry.Value.(string))
		}
	}

	message := int(body["message"].(float64))
	// add the message to the batch map
	s.batchLock.Lock()
	defer s.batchLock.Unlock()
	for _, dst := range neighbors {
		if dst == src || dst == s.NodeId {
			continue
		}
		s.BatchMap[dst] = append(s.BatchMap[dst], message)
	}
	return nil
}

func (s *server) batchBroadcast(src string, messages []int) error {
	s.topologyLock.RLock()
	// root node for the flat tree
	rootNode := s.Topology.GetNode(s.RootNodeId)
	defer s.topologyLock.RUnlock()

	var neighbors []string
	// get the neighbors of the root node
	if rootNode.Parent != nil {
		// append the parent node to the neighbors list
		neighbors = append(neighbors, rootNode.Parent.Entries[0].Value.(string))
	}

	// append the children nodes to the neighbors list
	for _, children := range rootNode.Children {
		for _, entry := range children.Entries {
			neighbors = append(neighbors, entry.Value.(string))
		}
	}
	// add the message to the batch map for each dst node
	s.batchLock.Lock()
	defer s.batchLock.Unlock()
	for _, dst := range neighbors {
		if dst == src || dst == s.NodeId {
			continue
		}
		// append the messages to the batch map
		s.BatchMap[dst] = append(s.BatchMap[dst], messages...)
	}
	return nil
}

func (s *server) batchRPC() {
	// broadcast the batch of messages to all the nodes
	// present in the batch map
	s.batchLock.Lock()
	defer s.batchLock.Unlock()

	wg := sync.WaitGroup{}
	for dst, message := range s.BatchMap {
		dst, message := dst, message
		go func() {
			// broadcast the message to the dst node
			err := s.rpcWithRetry(dst, map[string]any{
				"type":     "broadcast",
				"messages": message,
			}, MaxRetry)
			if err != nil {
				log.Error(err)
			}
		}()
	}
	// reset the batch map
	s.BatchMap = make(map[string][]int)
	// wait for all the rpc's to complete
	wg.Wait()

}

func (s *server) rpcWithRetry(dst string, body map[string]any, retry int) error {
	var err error
	for i := 0; i < retry; i++ {
		err = s.rpc(dst, body)
		if err == nil {
			return nil
		}
	}
	return err
}

func (s *server) rpc(dst string, body map[string]any) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := s.Node.SyncRPC(ctx, dst, body)
	return err
}

func (s *server) readHandler(msg maelstrom.Message) error {
	// get the list of all the seen message_ids
	seenMessageIds := s.getAllIDs()

	return s.Node.Reply(msg, map[string]any{
		"type":     "read_ok",
		"messages": seenMessageIds,
	})
}

func (s *server) getAllIDs() []int {
	s.messageIdsLock.RLock()
	ids := make([]int, 0, len(s.MessageIds))
	for id := range s.MessageIds {
		ids = append(ids, id)
	}
	s.messageIdsLock.RUnlock()

	return ids
}

func (s *server) topologyHandler(msg maelstrom.Message) error {
	tree := btree.NewWithIntComparator(len(s.Node.NodeIDs()))
	for i := 0; i < len(s.Node.NodeIDs()); i++ {
		// add the node id to the topology
		tree.Put(i, fmt.Sprintf("n%d", i))
	}

	s.topologyLock.Lock()
	s.Topology = tree
	s.topologyLock.Unlock()

	return s.Node.Reply(msg, map[string]any{
		"type": "topology_ok",
	})
}
