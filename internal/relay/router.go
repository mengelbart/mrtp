package relay

import (
	"errors"

	"github.com/mengelbart/mrtp/internal/container"
)

type Router struct {
	tracks *container.Trie[string, *Namespace]
}

func NewRouter() (*Router, error) {
	return &Router{
		tracks: container.NewTrie[string, *Namespace](),
	}, nil
}

func (r *Router) NewTrack(namespace []string, name string) (*Track, error) {
	ns, ok := r.tracks.Get(namespace)
	if !ok {
		ns = &Namespace{
			name:   namespace,
			tracks: map[string]*Track{},
		}
		r.tracks.Put(namespace, ns)
	}
	return ns.NewTrack(name)
}

func (r *Router) GetTrack(namespace []string, name string) (*Track, error) {
	ns, ok := r.tracks.Get(namespace)
	if !ok {
		return nil, errors.New("namespace does not exist")
	}
	return ns.GetTrack(name)
}
