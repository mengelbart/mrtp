package pipeline

import (
	"errors"
	"fmt"

	"github.com/mengelbart/mrtp"
)

// Tee copies a push input to N outputs.
type Tee[T any] struct {
	outputs []mrtp.Sink[T]
	format  mrtp.Format
}

func NewTee[T any]() *Tee[T] {
	return &Tee[T]{}
}

// AddOutput adds a branch. A branch added after the tee has been negotiated is
// negotiated immediately, with the format the tee carries.
func (t *Tee[T]) AddOutput(s mrtp.Sink[T]) error {
	t.outputs = append(t.outputs, s)
	if t.format != nil {
		return s.Negotiate(t.format)
	}
	return nil
}

// OwnedPorts implements OwnedPorts.
func (t *Tee[T]) OwnedPorts() []Edge {
	edges := make([]Edge, 0, len(t.outputs))
	for _, o := range t.outputs {
		edges = append(edges, Edge{Up: t, Down: o})
	}
	return edges
}

// Negotiate implements mrtp.Sink. It fans out to every branch and fails if any
// of them refuses.
func (t *Tee[T]) Negotiate(f mrtp.Format) error {
	for i, o := range t.outputs {
		if err := o.Negotiate(f); err != nil {
			return fmt.Errorf("tee output %d: %w", i, err)
		}
	}
	t.format = f
	return nil
}

// Write implements mrtp.Sink.
func (t *Tee[T]) Write(p mrtp.Packet[T]) error {
	var errs []error
	for _, o := range t.outputs {
		if err := o.Write(p.Clone()); err != nil {
			errs = append(errs, err)
		}
	}
	p.Release()
	return errors.Join(errs...)
}

// EndOfStream implements mrtp.Sink.
func (t *Tee[T]) EndOfStream() error {
	var errs []error
	for _, o := range t.outputs {
		if err := o.EndOfStream(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Close implements mrtp.Element. The branches are closed by the graph.
func (t *Tee[T]) Close() error {
	return nil
}

var (
	_ mrtp.Sink[int] = (*Tee[int])(nil)
	_ OwnedPorts     = (*Tee[int])(nil)
)
