package pubsub

import (
	"errors"
	"log"
	"sync"
)

type Channel struct {
	lock    sync.Mutex
	streams map[string]*stream
	closed  bool
}

func NewChannel() *Channel {
	c := &Channel{
		lock:    sync.Mutex{},
		streams: map[string]*stream{},
		closed:  false,
	}
	return c
}

func (c *Channel) NewPublisher() *Publisher {
	return newPublisher(c)
}

func (c *Channel) announce(stream string) error {
	c.lock.Lock()
	defer c.lock.Unlock()
	if c.closed {
		return errors.New("broker closed")
	}
	if _, ok := c.streams[stream]; ok {
		return errors.New("duplicate topic")
	}
	c.streams[stream] = newStream(stream)
	return nil
}

func (c *Channel) publish(stream string, msg Message) error {
	c.lock.Lock()
	defer c.lock.Unlock()
	t, ok := c.streams[stream]
	if !ok {
		return errors.New("unknown topic")
	}
	return t.publish(msg)
}

func (b *Channel) subscribe(stream string, subscriber *Subscriber) (int, error) {
	b.lock.Lock()
	defer b.lock.Unlock()
	t, ok := b.streams[stream]
	if !ok {
		return 0, errors.New("unknown topic")
	}
	return t.subscribe(subscriber), nil
}

func (b *Channel) unsubscribe(stream string, id int) error {
	b.lock.Lock()
	defer b.lock.Unlock()
	t, ok := b.streams[stream]
	if !ok {
		return errors.New("unknown topic")
	}
	t.unsubscribe(id)
	return nil
}

func (b *Channel) Close() error {
	log.Println("closing broker")
	b.lock.Lock()
	defer b.lock.Unlock()
	b.closed = true
	for _, t := range b.streams {
		if err := t.Close(); err != nil {
			log.Println(err)
		}
	}
	log.Println("channel closed")
	return nil
}
