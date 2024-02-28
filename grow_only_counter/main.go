package main

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"time"

	maelstrom "github.com/jepsen-io/maelstrom/demo/go"
	log "github.com/sirupsen/logrus"
)

const defaultTimeout = time.Second

func init() {
	f, err := os.OpenFile("/tmp/maelstrom.log", os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
	if err != nil {
		panic(err)
	}
	log.SetOutput(f)
}

func main() {
	newNode := maelstrom.NewNode()
	keyValueStore := maelstrom.NewSeqKV(newNode)
	server := &server{
		Node:       newNode,
		KVStore:    keyValueStore,
		LocalStore: make(map[string]int),
	}

	newNode.Handle("init", server.initHandler)
	newNode.Handle("add", server.addHandler)
	newNode.Handle("read", server.readHandler)
	newNode.Handle("local", server.localHandler)

	if err := newNode.Run(); err != nil {
		log.Fatal(err)
	}
}

type server struct {
	Node       *maelstrom.Node
	NodeID     string
	KVLock     sync.RWMutex
	KVStore    *maelstrom.KV
	LocalStore map[string]int // local cache incase of network partition
}

func (s *server) initHandler(_ maelstrom.Message) error {
	s.KVLock.Lock()
	s.NodeID = s.Node.ID()
	defer s.KVLock.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()
	// Initialize the key value store with 0
	if err := s.KVStore.Write(ctx, s.NodeID, 0); err != nil {
		log.Error(err)
		return err
	}
	return nil
}

func (s *server) addHandler(msg maelstrom.Message) error {
	var body map[string]any
	if err := json.Unmarshal(msg.Body, &body); err != nil {
		return err
	}

	delta := int(body["delta"].(float64))

	// Lock the key value store to prevent concurrent writes
	s.KVLock.Lock()
	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()
	// Read the current value from the key value store for the node
	sum, err := s.KVStore.ReadInt(ctx, s.NodeID)
	if err != nil {
		return err
	}

	ctx, cancel2 := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel2()
	// Write the new value (current + delta) to the key value store for the node
	err = s.KVStore.Write(ctx, s.NodeID, sum+delta)
	s.KVLock.Unlock()
	if err != nil {
		log.Error(err)
		return err
	}

	return s.Node.Reply(msg, map[string]any{
		"type": "add_ok",
	})
}

func (s *server) readHandler(msg maelstrom.Message) error {
	// initialize the sum to 0
	sum := 0
	// iterate over all the nodes and read the value from the key value store or contact the key store of other nodes for each node
	// and add it to the sum
	for _, nodeID := range s.Node.NodeIDs() {
		// if the node is the current node, read the value from the key value store
		if nodeID == s.NodeID {
			ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
			v, err := s.KVStore.ReadInt(ctx, s.NodeID)
			cancel()
			if err != nil {
				log.Warnf("failed to read %s: %v", s.NodeID, err)
				// as a fallback, use the local store
				//NOTE: LocalStore may not have the latest value, here we are optimizing for availability over consistency
				sum += s.LocalStore[nodeID]
				continue
			}
			sum += v
			// update the local store
			s.LocalStore[nodeID] = v
		} else {
			ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
			// contact the key value store of the other nodes
			res, err := s.Node.SyncRPC(ctx, nodeID, map[string]any{
				"type": "local",
			})
			cancel()
			if err != nil {
				log.Warnf("failed to call local endpoint %s from %s: %v", nodeID, s.NodeID, err)
				// as a fallback, use the local store
				//NOTE: LocalStore may not have the latest value, here we are optimizing for availability over consistency
				sum += s.LocalStore[nodeID]
				continue
			}

			var body map[string]any
			if err := json.Unmarshal(res.Body, &body); err != nil {
				return err
			}

			v := int(body["value"].(float64))
			sum += v
			// update the local store
			s.LocalStore[nodeID] = v
		}
	}

	return s.Node.Reply(msg, map[string]any{
		"type":  "read_ok",
		"value": sum,
	})
}

func (s *server) localHandler(msg maelstrom.Message) error {
	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()
	// read the value from the key value store for the node
	v, err := s.KVStore.ReadInt(ctx, s.NodeID)
	if err != nil {
		return err
	}

	return s.Node.Reply(msg, map[string]any{
		"type":  "local_ok",
		"value": v,
	})
}
