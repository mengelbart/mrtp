package pubsub

import (
	"errors"
	"iter"
	"sync"
)

type Subscriber struct {
	lock    sync.Mutex
	streams map[string]chan Message
	channel *Channel
}

func NewSubscriber(c *Channel) *Subscriber {
	return &Subscriber{
		lock:    sync.Mutex{},
		streams: map[string]chan Message{},
		channel: c,
	}
}

func (s *Subscriber) receive(stream string, msg Message) error {
	s.lock.Lock()
	defer s.lock.Unlock()
	t, ok := s.streams[stream]
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

func (s *Subscriber) Subscribe(stream string, queueSize int) (iter.Seq[Message], error) {
	s.lock.Lock()
	defer s.lock.Unlock()
	if _, ok := s.streams[stream]; ok {
		return nil, errors.New("duplicate subscription")
	}
	queue := make(chan Message, queueSize)
	s.streams[stream] = queue
	id, err := s.channel.subscribe(stream, s)
	if err != nil {
		delete(s.streams, stream)
		return nil, err
	}
	return func(yield func(Message) bool) {
		for msg := range queue {
			if !yield(msg) {
				s.channel.unsubscribe(stream, id)
				return
			}
		}
	}, nil
}
