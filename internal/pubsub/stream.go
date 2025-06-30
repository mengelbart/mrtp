package pubsub

import (
	"errors"
	"log"
	"sync"
)

type stream struct {
	name             string
	nextSubscriberID int
	lock             sync.Mutex
	queue            chan Message
	subscribers      map[int]*Subscriber
	wg               sync.WaitGroup
	closed           chan struct{}
}

func newStream(name string) *stream {
	t := &stream{
		name:             name,
		nextSubscriberID: 0,
		lock:             sync.Mutex{},
		queue:            make(chan Message),
		subscribers:      map[int]*Subscriber{},
		wg:               sync.WaitGroup{},
		closed:           make(chan struct{}),
	}
	t.wg.Add(1)
	go func() {
		defer t.wg.Done()
		t.process()
	}()
	return t
}

func (s *stream) subscribe(sub *Subscriber) int {
	s.lock.Lock()
	defer s.lock.Unlock()
	id := s.nextSubscriberID
	s.nextSubscriberID++
	s.subscribers[id] = sub
	return id
}

func (s *stream) unsubscribe(id int) {
	s.lock.Lock()
	defer s.lock.Unlock()
	delete(s.subscribers, id)
}

func (s *stream) publish(msg Message) error {
	select {
	case s.queue <- msg:
		return nil
	default:
		return errors.New("queue overflow")
	}
}

func (s *stream) process() {
	for {
		select {
		case msg := <-s.queue:
			s.fanout(msg)
		case <-s.closed:
			log.Printf("closing topic: %s", s.name)
			return
		}
	}
}

func (s *stream) fanout(msg Message) {
	s.lock.Lock()
	defer s.lock.Unlock()
	for _, sub := range s.subscribers {
		sub.receive(s.name, msg)
	}
}

func (s *stream) Close() error {
	close(s.closed)
	s.wg.Wait()
	return nil
}
