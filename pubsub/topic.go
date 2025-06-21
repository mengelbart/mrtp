package pubsub

import (
	"errors"
	"log"
	"sync"
)

type topic struct {
	name             string
	nextSubscriberID int
	lock             sync.Mutex
	queue            chan Message
	subscribers      map[int]*Subscriber
	wg               sync.WaitGroup
	closed           chan struct{}
}

func newTopic(name string) *topic {
	t := &topic{
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

func (t *topic) subscribe(s *Subscriber) int {
	t.lock.Lock()
	defer t.lock.Unlock()
	id := t.nextSubscriberID
	t.nextSubscriberID++
	t.subscribers[id] = s
	return id
}

func (t *topic) unsubscribe(id int) {
	t.lock.Lock()
	defer t.lock.Unlock()
	delete(t.subscribers, id)
}

func (t *topic) publish(msg Message) error {
	select {
	case t.queue <- msg:
		return nil
	default:
		return errors.New("queue overflow")
	}
}

func (t *topic) process() {
	for {
		select {
		case msg := <-t.queue:
			t.fanout(msg)
		case <-t.closed:
			log.Printf("closing topic: %s", t.name)
			return
		}
	}
}

func (t *topic) fanout(msg Message) {
	t.lock.Lock()
	defer t.lock.Unlock()
	for _, s := range t.subscribers {
		s.receive(t.name, msg)
	}
}

func (t *topic) Close() error {
	close(t.closed)
	t.wg.Wait()
	return nil
}
