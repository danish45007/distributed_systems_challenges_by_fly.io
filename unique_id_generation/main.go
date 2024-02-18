package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"time"

	maelstrom "github.com/jepsen-io/maelstrom/demo/go"
)

func main() {
	newNode := maelstrom.NewNode()

	newNode.Handle("generate", func(msg maelstrom.Message) error {
		var body map[string]interface{}
		if err := json.Unmarshal(msg.Body, &body); err != nil {
			return err
		}

		body["type"] = "generate_ok"
		// generate a random number
		body["id"] = fmt.Sprintf("%v%v", time.Now().UnixNano(), rand.Intn(10000))
		return newNode.Reply(msg, body)
	})

	if err := newNode.Run(); err != nil {
		log.Fatal(err)
	}
}
