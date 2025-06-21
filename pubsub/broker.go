package pubsub

import (
	"errors"
	"log"
	"sync"
)

type publication struct {
	topic string
	msg   Message
}

type Broker struct {
	lock   sync.Mutex
	queue  chan publication
	topics map[string]*topic
	closed bool
}

func NewBroker() *Broker {
	b := &Broker{
		lock:   sync.Mutex{},
		queue:  make(chan publication),
		topics: map[string]*topic{},
		closed: false,
	}
	return b
}

func (b *Broker) announce(topic string) error {
	b.lock.Lock()
	defer b.lock.Unlock()
	if b.closed {
		return errors.New("broker closed")
	}
	if _, ok := b.topics[topic]; ok {
		return errors.New("duplicate topic")
	}
	b.topics[topic] = newTopic(topic)
	return nil
}

func (b *Broker) publish(topic string, msg Message) error {
	b.lock.Lock()
	defer b.lock.Unlock()
	t, ok := b.topics[topic]
	if !ok {
		return errors.New("unknown topic")
	}
	return t.publish(msg)
}

func (b *Broker) subscribe(topic string, subscriber *Subscriber) (int, error) {
	b.lock.Lock()
	defer b.lock.Unlock()
	t, ok := b.topics[topic]
	if !ok {
		return 0, errors.New("unknown topic")
	}
	return t.subscribe(subscriber), nil
}

func (b *Broker) unsubscribe(topic string, id int) error {
	b.lock.Lock()
	defer b.lock.Unlock()
	t, ok := b.topics[topic]
	if !ok {
		return errors.New("unknown topic")
	}
	t.unsubscribe(id)
	return nil
}

func (b *Broker) Close() error {
	log.Println("closing broker")
	b.lock.Lock()
	defer b.lock.Unlock()
	b.closed = true
	for _, t := range b.topics {
		if err := t.Close(); err != nil {
			log.Println(err)
		}
	}
	log.Println("broker closed")
	return nil
}
