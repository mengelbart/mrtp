package relay

import "errors"

type Namespace struct {
	name   []string
	tracks map[string]*Track
}

func (n *Namespace) NewTrack(name string) (*Track, error) {
	if _, ok := n.tracks[name]; ok {
		return nil, errors.New("duplicate track name")
	}
	t := newTrack(name)
	n.tracks[name] = t
	return t, nil
}

func (n *Namespace) GetTrack(name string) (*Track, error) {
	t, ok := n.tracks[name]
	if !ok {
		return nil, errors.New("track not found")
	}
	return t, nil
}
