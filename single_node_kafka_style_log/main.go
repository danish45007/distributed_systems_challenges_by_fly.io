package main

import (
	"encoding/json"
	"strconv"
	"sync"

	maelstrom "github.com/jepsen-io/maelstrom/demo/go"
	log "github.com/sirupsen/logrus"
)

// LogWrapper is a higher order function that takes a function as an argument and returns a function
// that logs any error returned by the function
func LogWrapper(f func(msg maelstrom.Message) error) func(msg maelstrom.Message) error {
	return func(msg maelstrom.Message) error {
		if err := f(msg); err != nil {
			log.Error(err)
			return err
		}
		return nil
	}

}

type offsetRequest struct {
	Offsets map[string]int `json:"offsets"`
}

type entry struct {
	offset  int
	message int
}

type server struct {
	Node             *maelstrom.Node
	NodeID           string
	id               int
	LockLogEntries   sync.RWMutex
	Logs             map[string][]entry
	CommittedOffsets map[string]int
	LatestOffsets    map[string]int
}

func main() {
	newNode := maelstrom.NewNode()
	server := &server{
		Node:             newNode,
		Logs:             make(map[string][]entry),
		CommittedOffsets: make(map[string]int),
		LatestOffsets:    make(map[string]int),
	}
	newNode.Handle("init", LogWrapper(server.initHandler))
	newNode.Handle("send", LogWrapper(server.sendHandler))
	newNode.Handle("poll", LogWrapper(server.pollHandler))
	newNode.Handle("commit_offsets", LogWrapper(server.commitHandler))
	newNode.Handle("list_committed_offsets", LogWrapper(server.listCommittedOffsetsHandler))

	if err := newNode.Run(); err != nil {
		log.Fatal(err)
	}
}

func (s *server) initHandler(msg maelstrom.Message) error {
	s.NodeID = s.Node.ID()
	id, err := strconv.Atoi(s.NodeID[1:])
	if err != nil {
		return err
	}
	s.id = id
	return nil
}

func (s *server) sendHandler(msg maelstrom.Message) error {
	var body map[string]any
	if err := json.Unmarshal(msg.Body, &body); err != nil {
		return err
	}

	key := body["key"].(string)
	message := int(body["msg"].(float64))

	s.LockLogEntries.Lock()
	offset := s.LatestOffsets[key] + 1
	s.Logs[key] = append(s.Logs[key], entry{
		offset:  offset,
		message: message,
	})
	s.LatestOffsets[key] = offset
	s.LockLogEntries.Unlock()

	return s.Node.Reply(msg, map[string]any{
		"type":   "send_ok",
		"offset": offset,
	})
}

/*
logs {
	"1": [{
		"offset": 1,
		"message": 1
	},{
		"offset": 1,
		"message": 2
	},{
		"offset": 1,
		"message": 3
	},{
		"offset": 2,
		"message": 4
	},{
		"offset": 2,
		"message": 4
	},{
		"offset": 3,
		"message": 4
	}]
}

req {
	"key": 1
	"offset": 2
}

res {
	"key1": [[1, 1], [1, 2], [1, 3]]
}

*/

// using binary search to find the index of the first entry with offset greater than or equal to startOffset
func (s *server) getOffsetIdx(entities []entry, startOffset int) int {
	left, right := 0, len(entities)-1
	for left <= right {
		// find the middle index overflows for large values
		mid := left + (right-left)/2
		if entities[mid].offset == startOffset {
			return mid
		}
		if entities[mid].offset < startOffset {
			left = mid + 1
		} else {
			right = mid - 1
		}
	}
	if left == 0 {
		return 0
	}
	return left
}
func (s *server) pollHandler(msg maelstrom.Message) error {
	var req offsetRequest
	if err := json.Unmarshal(msg.Body, &req); err != nil {
		return err
	}
	s.LockLogEntries.Lock()
	defer s.LockLogEntries.Unlock()
	response := make(map[string][][2]int)
	for key, startOffset := range req.Offsets {
		entities := s.Logs[key]
		// find the index of the first entry with offset greater than or equal to startOffset
		offsetIdx := s.getOffsetIdx(entities, startOffset)
		for ; offsetIdx < len(entities); offsetIdx++ {
			entity := entities[offsetIdx]
			response[key] = append(response[key], [2]int{entity.offset, entity.message})
		}
	}
	return s.Node.Reply(msg, map[string]interface{}{
		"type": "poll_ok",
		"msgs": response,
	})
}

func (s *server) commitHandler(msg maelstrom.Message) error {
	var req offsetRequest
	if err := json.Unmarshal(msg.Body, &req); err != nil {
		return err
	}
	s.LockLogEntries.Lock()
	defer s.LockLogEntries.Unlock()
	for key, offset := range req.Offsets {
		s.CommittedOffsets[key] = offset
	}
	return s.Node.Reply(msg, map[string]interface{}{
		"type": "commit_offsets_ok",
	})
}

func (s *server) listCommittedOffsetsHandler(msg maelstrom.Message) error {
	var body map[string]interface{}
	if err := json.Unmarshal(msg.Body, &body); err != nil {
		return err
	}
	keys := body["keys"].([]interface{})
	offsetResponse := make(map[string]int)
	for _, key := range keys {
		k := key.(string)
		offsetResponse[k] = s.CommittedOffsets[k]
	}
	return s.Node.Reply(msg, map[string]interface{}{
		"type":    "list_committed_offsets_ok",
		"offsets": offsetResponse,
	})

}
