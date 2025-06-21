package pubsub

import (
	"errors"
	"iter"
	"sync"
)

type Subscriber struct {
	lock   sync.Mutex
	topics map[string]chan Message
	broker *Broker
}

func NewSubscriber(b *Broker) *Subscriber {
	return &Subscriber{
		lock:   sync.Mutex{},
		topics: map[string]chan Message{},
		broker: b,
	}
}

func (s *Subscriber) receive(topic string, msg Message) error {
	s.lock.Lock()
	defer s.lock.Unlock()
	t, ok := s.topics[topic]
	if !ok {
		return errors.New("got unexpected message")
	}
	select {
	case t <- msg:
		return nil
	default:
		return errors.New("queue overflow")
	}
}

func (s *Subscriber) Subscribe(topic string, queueSize int) (iter.Seq[Message], error) {
	s.lock.Lock()
	defer s.lock.Unlock()
	if _, ok := s.topics[topic]; ok {
		return nil, errors.New("duplicate subscription")
	}
	queue := make(chan Message, queueSize)
	s.topics[topic] = queue
	id, err := s.broker.subscribe(topic, s)
	if err != nil {
		delete(s.topics, topic)
		return nil, err
	}
	return func(yield func(Message) bool) {
		for msg := range queue {
			if !yield(msg) {
				s.broker.unsubscribe(topic, id)
				return
			}
		}
	}, nil
}
