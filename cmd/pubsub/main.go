package main

import (
	"log"
	"time"

	"github.com/mengelbart/mrtp/pubsub"
)

func main() {
	broker := pubsub.NewBroker()
	defer broker.Close()

	pub := pubsub.NewPublisher(broker)
	if err := pub.Announce("A"); err != nil {
		panic(err)
	}

	subscriber1 := pubsub.NewSubscriber(broker)
	subscription1, err := subscriber1.Subscribe("A", 100)
	if err != nil {
		panic(err)
	}

	subscriber2 := pubsub.NewSubscriber(broker)
	subscription2, err := subscriber2.Subscribe("A", 200)
	if err != nil {
		panic(err)
	}

	go func() {
		for m := range subscription1 {
			log.Printf("subscriber 1 got message: %s", m.Payload)
		}
	}()
	go func() {
		for m := range subscription2 {
			log.Printf("subscriber 2 got message: %s", m.Payload)
		}
	}()

	time.Sleep(time.Second)
	pub.Publish("A", pubsub.Message{
		Payload: []byte("hello world!"),
	})
	time.Sleep(time.Second)
}
