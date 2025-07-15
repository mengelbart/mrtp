package moq

import (
	"errors"
	"sync"

	"github.com/mengelbart/moqtransport"
)

type LocalTrack struct {
	nextID      int
	lock        sync.Mutex
	subscribers map[int]moqtransport.Publisher
	closed      bool
}

func NewLocalTrack() *LocalTrack {
	return &LocalTrack{
		nextID:      0,
		lock:        sync.Mutex{},
		subscribers: map[int]moqtransport.Publisher{},
	}
}

func (t *LocalTrack) subscribe(publisher moqtransport.Publisher) {
	t.lock.Lock()
	defer t.lock.Unlock()
	id := t.nextID
	t.nextID++
	t.subscribers[id] = publisher
}

func (t *LocalTrack) Write(data []byte) (int, error) {
	t.lock.Lock()
	defer t.lock.Unlock()
	if t.closed {
		return 0, errors.New("track closed")
	}
	for _, p := range t.subscribers {
		sg, err := p.OpenSubgroup(0, 0, 0)
		if err != nil {
			// TODO
			continue
		}
		_, err = sg.WriteObject(0, data)
		if err != nil {
			// TODO
			continue
		}
	}
	return 0, nil
}

func (t *LocalTrack) Close() error {
	t.lock.Lock()
	defer t.lock.Unlock()
	t.closed = true
	for _, p := range t.subscribers {
		_ = p.CloseWithError(0, "track closed")
	}
	return nil
}
