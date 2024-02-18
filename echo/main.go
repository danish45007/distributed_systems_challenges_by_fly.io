package main

import (
	"encoding/json"
	"log"

	maelstrom "github.com/jepsen-io/maelstrom/demo/go"
)

func main() {
	newNode := maelstrom.NewNode()

	newNode.Handle("echo", func(msg maelstrom.Message) error {
		var body map[string]interface{}
		if err := json.Unmarshal(msg.Body, &body); err != nil {
			return err
		}

		body["type"] = "echo_ok"
		return newNode.Reply(msg, body)
	})

	if err := newNode.Run(); err != nil {
		log.Fatal(err)
	}
}
