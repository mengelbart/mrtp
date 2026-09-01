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

func (c *Channel) subscribe(stream string, subscriber *Subscriber) (int, error) {
	c.lock.Lock()
	defer c.lock.Unlock()
	t, ok := c.streams[stream]
	if !ok {
		return 0, errors.New("unknown topic")
	}
	return t.subscribe(subscriber), nil
}

func (c *Channel) unsubscribe(stream string, id int) error {
	c.lock.Lock()
	defer c.lock.Unlock()
	t, ok := c.streams[stream]
	if !ok {
		return errors.New("unknown topic")
	}
	t.unsubscribe(id)
	return nil
}

func (c *Channel) Close() error {
	log.Println("closing broker")
	c.lock.Lock()
	defer c.lock.Unlock()
	c.closed = true
	for _, t := range c.streams {
		if err := t.Close(); err != nil {
			log.Println(err)
		}
	}
	log.Println("channel closed")
	return nil
}
