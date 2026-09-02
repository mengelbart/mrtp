package pipeline

import (
	"errors"
	"fmt"
	"sync"

	"github.com/mengelbart/mrtp"
)

// Funnel merges N push inputs into one push output. Input returns a fresh sink
// per producer, so producers stay independent and each can be driven by its own
// segment, and ordering across inputs is arrival order.
//
// The funnel serialises its inputs, so a caller who must not let a slow output
// block a fast input puts a [Queue] in front of the funnel's output.
type Funnel[T any] struct {
	mu     sync.Mutex
	inputs []*funnelInput[T]
	down   mrtp.Sink[T]
	format mrtp.Format
	ended  int
}

func NewFunnel[T any]() *Funnel[T] {
	return &Funnel[T]{}
}

// Input returns a sink for one producer. Every input must negotiate the same
// format, because they share one output.
func (f *Funnel[T]) Input() mrtp.Sink[T] {
	f.mu.Lock()
	defer f.mu.Unlock()
	in := &funnelInput[T]{funnel: f}
	f.inputs = append(f.inputs, in)
	return in
}

// OwnedPorts implements [OwnedPorts].
func (f *Funnel[T]) OwnedPorts() []Edge {
	f.mu.Lock()
	defer f.mu.Unlock()
	edges := make([]Edge, 0, len(f.inputs))
	for _, in := range f.inputs {
		edges = append(edges, Edge{Up: in, Down: f})
	}
	return edges
}

// Format implements mrtp.Source. It is what the inputs agreed on.
func (f *Funnel[T]) Format() mrtp.Format {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.format
}

// Connect implements mrtp.Source.
func (f *Funnel[T]) Connect(s mrtp.Sink[T]) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down != nil {
		return errors.New("pipeline: funnel is already connected")
	}
	f.down = s
	return nil
}

// Close implements mrtp.Element.
func (f *Funnel[T]) Close() error {
	return nil
}

func (f *Funnel[T]) negotiate(format mrtp.Format) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.format == nil {
		f.format = format
		return nil
	}
	if f.format != format {
		return fmt.Errorf("funnel carries %v, not %v", f.format, format)
	}
	return nil
}

func (f *Funnel[T]) write(p mrtp.Packet[T]) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down == nil {
		p.Release()
		return errors.New("pipeline: funnel has no output")
	}
	return f.down.Write(p)
}

// endOfStream forwards the end of the stream once every input has ended.
func (f *Funnel[T]) endOfStream() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ended++
	if f.ended < len(f.inputs) || f.down == nil {
		return nil
	}
	return f.down.EndOfStream()
}

// funnelInput is one producer's port into a funnel.
type funnelInput[T any] struct {
	funnel *Funnel[T]
}

func (i *funnelInput[T]) Negotiate(f mrtp.Format) error { return i.funnel.negotiate(f) }
func (i *funnelInput[T]) Write(p mrtp.Packet[T]) error  { return i.funnel.write(p) }
func (i *funnelInput[T]) EndOfStream() error            { return i.funnel.endOfStream() }
func (i *funnelInput[T]) Close() error                  { return nil }

var (
	_ mrtp.Source[int] = (*Funnel[int])(nil)
	_ OwnedPorts       = (*Funnel[int])(nil)
	_ mrtp.Sink[int]   = (*funnelInput[int])(nil)
)
