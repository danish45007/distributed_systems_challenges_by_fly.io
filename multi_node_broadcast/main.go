package main

import (
	"encoding/json"
	"log"
	"sync"

	maelstrom "github.com/jepsen-io/maelstrom/demo/go"
)

type server struct {
	Node   *maelstrom.Node
	NodeID string
	// message
	message_ids_lock sync.RWMutex
	message_ids      map[int]struct{} // list of message ids seen
}

func main() {
	newNode := maelstrom.NewNode()
	server := &server{
		Node:        newNode,
		NodeID:      newNode.ID(),
		message_ids: make(map[int]struct{}),
	}
	newNode.Handle("broadcast", server.broadcastHandler)
	newNode.Handle("read", server.readHandler)
	newNode.Handle("topology", server.topologyHandler)
	if err := newNode.Run(); err != nil {
		log.Fatal(err)
	}
}

func (s *server) broadcastHandler(msg maelstrom.Message) error {
	var responseMessage map[string]interface{}
	// unmarshal the message
	if err := json.Unmarshal(msg.Body, &responseMessage); err != nil {
		return err
	}
	// check if the message has been seen
	messageId := int(responseMessage["message"].(float64))
	s.message_ids_lock.Lock()
	if _, seen := s.message_ids[messageId]; seen {
		s.message_ids_lock.Unlock()
		return nil
	}
	s.message_ids[messageId] = struct{}{}
	s.message_ids_lock.Unlock()
	// broadcast the message to all other nodes
	if err := s.broadcastMessage(responseMessage, s.NodeID); err != nil {
		return err
	}
	return s.Node.Reply(msg, map[string]interface{}{"type": "broadcast_ok"})

}

func (s *server) broadcastMessage(message map[string]interface{}, srcNode string) error {
	// broadcast the message to all other nodes
	for _, node := range s.Node.NodeIDs() {
		// check if the node is the source node or the id of the node is same as the source node id
		if srcNode == node || node == s.NodeID {
			continue
		}
		// create a closure to capture the node
		localNode := node
		// send the message to the node
		go func() {
			err := s.Node.Send(localNode, message)
			if err != nil {
				panic(err)
			}
		}()
	}
	return nil
}

func (s *server) readHandler(msg maelstrom.Message) error {
	// get the list of all the seen message_ids
	message_ids := s.GetAllMessageIds()
	return s.Node.Reply(msg, map[string]interface{}{
		"type":     "read_ok",
		"messages": message_ids,
	})
}

func (s *server) GetAllMessageIds() []int {
	s.message_ids_lock.RLock()
	seen_message_ids := make([]int, 0, len(s.message_ids))
	for id := range s.message_ids {
		seen_message_ids = append(seen_message_ids, id)
	}
	s.message_ids_lock.RUnlock()
	return seen_message_ids
}

func (s *server) topologyHandler(msg maelstrom.Message) error {
	return s.Node.Reply(msg, map[string]interface{}{
		"type": "topology_ok",
	})
}
