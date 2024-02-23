package main

import (
	"context"
	"encoding/json"
	"log"
	"sync"

	maelstrom "github.com/jepsen-io/maelstrom/demo/go"
)

type broadcastMessage struct {
	message  map[string]interface{}
	destNode string
}

type broadcaster struct {
	cancel                  context.CancelFunc
	broadcastMessageChannel chan broadcastMessage
}

type server struct {
	Node        *maelstrom.Node
	NodeID      string
	Broadcaster *broadcaster
	// message
	message_ids_lock sync.RWMutex
	message_ids      map[int]struct{} // list of message ids seen
}

func newBroadcaster(node *maelstrom.Node, workers int) *broadcaster {
	broadcastMessageChannel := make(chan broadcastMessage)
	ctx, cancel := context.WithCancel(context.Background())
	// create workers to broadcast messages
	for i := 0; i < workers; i++ {
		go func() {
			for {
				select {
				case messageReceived := <-broadcastMessageChannel: // receive message to broadcast
					for {
						if err := node.Send(messageReceived.destNode, messageReceived.message); err != nil {
							// error could be due to the node not being available due to network partition
							continue
						}
						break
					}
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	return &broadcaster{
		cancel:                  cancel,
		broadcastMessageChannel: broadcastMessageChannel,
	}
}
func main() {
	newNode := maelstrom.NewNode()
	// create a new broadcaster with 15 workers to broadcast messages
	broadcaster := newBroadcaster(newNode, 10)
	// cancel the broadcaster when the execution of the main function ends
	defer broadcaster.close()
	server := &server{
		Node:        newNode,
		NodeID:      newNode.ID(),
		message_ids: make(map[int]struct{}),
		Broadcaster: broadcaster,
	}
	newNode.Handle("broadcast", server.broadcastHandler)
	newNode.Handle("read", server.readHandler)
	newNode.Handle("topology", server.topologyHandler)
	if err := newNode.Run(); err != nil {
		log.Fatal(err)
	}
}

func (s *server) broadcastMessage(message map[string]interface{}, srcNode string) {
	// broadcast the message to all other nodes
	for _, destNode := range s.Node.NodeIDs() {
		if destNode == srcNode || destNode == s.NodeID {
			continue
		}
		s.Broadcaster.broadcast(broadcastMessage{
			message:  message,
			destNode: destNode,
		})
	}

}

func (s *server) broadcastHandler(message maelstrom.Message) error {
	var responseMessage map[string]interface{}
	// unmarshal the message
	if err := json.Unmarshal(message.Body, &responseMessage); err != nil {
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
	s.broadcastMessage(responseMessage, s.NodeID)
	return s.Node.Reply(message, map[string]interface{}{"type": "broadcast_ok"})
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

func (s *server) topologyHandler(message maelstrom.Message) error {
	return s.Node.Reply(message, map[string]interface{}{
		"type": "topology_ok",
	})
}

func (br *broadcaster) broadcast(message broadcastMessage) {
	br.broadcastMessageChannel <- message
}

func (br *broadcaster) close() {
	br.cancel()
}
