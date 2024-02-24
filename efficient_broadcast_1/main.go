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
const MaxRetries = 100

type server struct {
	Node       *maelstrom.Node
	NodeID     string
	RootNodeID int
	// message
	messageIDsLock sync.RWMutex
	MessageIDs     map[int]struct{}

	// topology
	topologyLock sync.RWMutex
	Topology     *btree.Tree // flat tree (root -> [c1,c2....]): efficient data structure to store the topology of the network
}

// Logging the output to a file
func init() {
	file, err := os.OpenFile("./maelstrom.log", os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
	if err != nil {
		panic(err)
	}
	log.SetOutput(file)
}

func main() {
	newNode := maelstrom.NewNode()
	server := &server{
		Node:       newNode,
		MessageIDs: make(map[int]struct{}),
	}
	newNode.Handle("init", server.initHandler)
	newNode.Handle("broadcast", server.broadcastHandler)
	newNode.Handle("read", server.readHandler)
	newNode.Handle("topology", server.topologyHandler)
}

// initHandler initializes the server
func (s *server) initHandler(_ maelstrom.Message) error {
	s.NodeID = s.Node.ID()
	// node with the lowest id is the root node
	// nodeId is the node id without the prefix "n"
	nodeId, err := strconv.Atoi(s.NodeID[1:])
	if err != nil {
		return err
	}
	s.RootNodeID = nodeId
	return nil
}

func (s *server) broadcastHandler(msg maelstrom.Message) error {
	var responseMessage map[string]interface{}
	// unmarshal the message
	if err := json.Unmarshal(msg.Body, &responseMessage); err != nil {
		return err
	}
	// return back the message in a separate go routine
	go func() {
		_ = s.Node.Reply(msg, map[string]interface{}{
			"type": "broadcast_ok",
		})
	}()
	// check if the message has been seen
	messageID := int(responseMessage["message"].(float64))
	s.messageIDsLock.Lock()
	if _, seen := s.MessageIDs[messageID]; seen {
		s.messageIDsLock.Unlock()
		return nil
	}
	// mark it as seen
	s.MessageIDs[messageID] = struct{}{}
	s.messageIDsLock.Unlock()
	// broadcast the message to all other nodes
	return s.broadcast(responseMessage, msg.Src)
}

func (s *server) broadcast(message map[string]interface{}, srcNode string) error {
	s.topologyLock.RLock()
	// root node for the flat tree
	rootNode := s.Topology.GetNode(s.RootNodeID)
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
			// append the value of the entry to the neighbors list
			neighbors = append(neighbors, entry.Value.(string))
		}
	}
	for _, dst := range neighbors {
		if dst == srcNode || dst == s.NodeID {
			continue
		}
		// create a closure to capture the dst
		dst := dst
		// send the message to the dst
		go func() {
			if err := s.rpc(message, dst); err != nil {
				// in case of n/w failure, retry sending the message
				for i := 0; i < MaxRetries; i++ {
					if err := s.rpc(message, dst); err != nil {
						// sleep for 1 second before retrying
						time.Sleep(time.Duration(i) * time.Second)
						continue
					}
					return
				}
				// log the error
				log.Error(err)
			}
		}()
	}
	return nil
}

func (s *server) rpc(message map[string]interface{}, dst string) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := s.Node.SyncRPC(ctx, dst, message)
	return err
}

func (s *server) readHandler(msg maelstrom.Message) error {
	// get the list of all the seen message_ids
	messageIDs := s.GetAllMessageIds()
	return s.Node.Reply(msg, map[string]interface{}{
		"type":     "read_ok",
		"messages": messageIDs,
	})
}

func (s *server) GetAllMessageIds() []int {
	s.messageIDsLock.RLock()
	seenMessageIDs := make([]int, 0, len(s.MessageIDs))
	for id := range s.MessageIDs {
		seenMessageIDs = append(seenMessageIDs, id)
	}
	s.messageIDsLock.RUnlock()
	return seenMessageIDs
}

func (s *server) topologyHandler(msg maelstrom.Message) error {
	topology := btree.NewWithIntComparator(len(s.Node.NodeIDs()))
	for i := 0; i < len(s.Node.NodeIDs()); i++ {
		// add the node id to the topology
		nodeWithPrefix := fmt.Sprintf("n%d", i)
		topology.Put(i, nodeWithPrefix)
	}
	s.topologyLock.Lock()
	s.Topology = topology
	s.topologyLock.Unlock()
	return s.Node.Reply(msg, map[string]interface{}{
		"type": "topology_ok",
	})
}
