package main

import (
	"encoding/json"
	"log"
	"sync"

	maelstrom "github.com/jepsen-io/maelstrom/demo/go"
)

type server struct {
	Node *maelstrom.Node

	// message
	message_ids_lock sync.RWMutex
	message_ids      []int // list of message ids

	// topology
	topology_lock sync.RWMutex
	topology      map[string][]string // map of node id to list of node ids
}

type Topology struct {
	Topology map[string][]string `json:"topology"`
}

func main() {
	newNode := maelstrom.NewNode()
	server := &server{
		Node: newNode,
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
	// add the message to the list of message ids
	s.message_ids_lock.Lock()
	s.message_ids = append(s.message_ids, int(responseMessage["message"].(float64)))
	s.message_ids_lock.Unlock()
	// return back the message
	return s.Node.Reply(msg, map[string]interface{}{"type": "broadcast_ok"})
}

func (s *server) readHandler(msg maelstrom.Message) error {
	s.message_ids_lock.RLock()
	ids := make([]int, len(s.message_ids))
	copy(ids, s.message_ids)
	s.message_ids_lock.RUnlock()

	return s.Node.Reply(msg, map[string]interface{}{
		"type":     "read_ok",
		"messages": ids,
	})
}

func (s *server) topologyHandler(msg maelstrom.Message) error {
	var t Topology
	if err := json.Unmarshal(msg.Body, &t); err != nil {
		return err
	}
	// read the topology
	s.topology_lock.RLock()
	s.topology = t.Topology
	s.topology_lock.RUnlock()
	// return the topology
	return s.Node.Reply(msg, map[string]interface{}{
		"type": "topology_ok",
	})
}
