package pipeline

import (
	"sync/atomic"

	"github.com/mengelbart/mrtp"
)

// Discard is a push sink that releases every packet it is written and counts
// them.
type Discard[T any] struct {
	packets atomic.Uint64
}

func NewDiscard[T any]() *Discard[T] {
	return &Discard[T]{}
}

// Packets returns the number of packets written so far.
func (d *Discard[T]) Packets() uint64 {
	return d.packets.Load()
}

// Negotiate implements mrtp.Sink. It accepts any format.
func (d *Discard[T]) Negotiate(mrtp.Format) error {
	return nil
}

// Write implements mrtp.Sink.
func (d *Discard[T]) Write(p mrtp.Packet[T]) error {
	p.Release()
	d.packets.Add(1)
	return nil
}

// EndOfStream implements mrtp.Sink.
func (d *Discard[T]) EndOfStream() error {
	return nil
}

// Close implements mrtp.Element.
func (d *Discard[T]) Close() error {
	return nil
}

var _ mrtp.Sink[mrtp.DataChunk] = (*Discard[mrtp.DataChunk])(nil)
