package signaling

import (
	"slices"
	"sync"
)

type Candidates struct {
	lock    sync.Mutex
	list    []ICECandidate
	ended   bool
	changed chan struct{}
}

func NewCandidates() *Candidates {
	return &Candidates{changed: make(chan struct{})}
}

// Push appends candidate, or ends the list if candidate is nil. Candidates
// pushed after the end are dropped.
func (c *Candidates) Push(candidate *ICECandidate) {
	c.lock.Lock()
	defer c.lock.Unlock()
	if c.ended {
		return
	}
	if candidate == nil {
		c.ended = true
	} else {
		c.list = append(c.list, *candidate)
	}
	close(c.changed)
	c.changed = make(chan struct{})
}

// since returns the candidates from index i on, whether the list has ended,
// and a channel that is closed on the next change.
func (c *Candidates) since(i int) ([]ICECandidate, bool, <-chan struct{}) {
	c.lock.Lock()
	defer c.lock.Unlock()
	return slices.Clone(c.list[i:]), c.ended, c.changed
}
