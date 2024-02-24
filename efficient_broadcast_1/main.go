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
{
  "type": "topology",
  "topology": {
    "n1": ["n2", "n3"],
    "n2": ["n1"],
    "n3": ["n1"]
  }
}
 n1 -> n2, n3
 n2 -> n1 // not required
 n3 -> n1 // not required
 n2 ->n3
 n2 -> n1
 n1 -> n3
max distance = 2
 n1(root)
 n2 n3
*/

// MaxRetries is the maximum number of retries in case of n/w failure
const MaxRetry = 100

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
	server := &server{Node: newNode, MessageIds: make(map[int]struct{})}

	newNode.Handle("init", server.initHandler)
	newNode.Handle("broadcast", server.broadcastHandler)
	newNode.Handle("read", server.readHandler)
	newNode.Handle("topology", server.topologyHandler)

	if err := newNode.Run(); err != nil {
		log.Fatal(err)
	}
}

// initHandler initializes the server
func (s *server) initHandler(_ maelstrom.Message) error {
	s.NodeId = s.Node.ID()
	// node with the lowest id is the root node
	// nodeId is the node id without the prefix "n"
	nodeId, err := strconv.Atoi(s.NodeId[1:])
	if err != nil {
		return err
	}
	s.RootNodeId = nodeId
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

	for _, dst := range neighbors {
		if dst == src || dst == s.NodeId {
			continue
		}

		dst := dst
		// send the message to the dst node
		go func() {
			if err := s.rpc(dst, body); err != nil {
				// in case of n/w failure, retry sending the message
				for i := 0; i < MaxRetry; i++ {
					if err := s.rpc(dst, body); err != nil {
						// Sleep and retry
						time.Sleep(time.Duration(i) * time.Second)
						continue
					}
					return
				}
				log.Error(err)
			}
		}()
	}
	return nil
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
