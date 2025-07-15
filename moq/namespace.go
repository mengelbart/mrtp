package moq

import (
	"errors"
	"sync"
)

type namespace struct {
	lock   sync.Mutex
	tracks map[string]*LocalTrack
}

func newNamespace() *namespace {
	return &namespace{
		tracks: map[string]*LocalTrack{},
		lock:   sync.Mutex{},
	}
}

func (n *namespace) findTrack(name string) *LocalTrack {
	n.lock.Lock()
	defer n.lock.Unlock()
	t, ok := n.tracks[name]
	if !ok {
		return nil
	}
	return t
}

func (n *namespace) addTrack(name string, track *LocalTrack) error {
	n.lock.Lock()
	defer n.lock.Unlock()
	if _, ok := n.tracks[name]; ok {
		return errors.New("duplicate track")
	}
	n.tracks[name] = track
	return nil
}
