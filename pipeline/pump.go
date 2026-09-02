package pipeline

import (
	"context"
	"errors"
	"io"

	"github.com/mengelbart/mrtp"
)

// Pump drives a pulling upstream into a pushing downstream. It buffers nothing,
// it only supplies the goroutine that neither side has.
type Pump[T any] struct {
	up   mrtp.Puller[T]
	down mrtp.Sink[T]
}

func NewPump[T any]() *Pump[T] {
	return &Pump[T]{}
}

// Attach implements mrtp.Consumer.
func (p *Pump[T]) Attach(up mrtp.Puller[T]) error {
	if p.up != nil {
		return errors.New("pipeline: pump is already attached")
	}
	p.up = up
	return nil
}

// Format implements mrtp.Source. A pump passes on what it pulls.
func (p *Pump[T]) Format() mrtp.Format {
	if p.up == nil {
		return nil
	}
	return p.up.Format()
}

// Connect implements mrtp.Source.
func (p *Pump[T]) Connect(s mrtp.Sink[T]) error {
	if p.down != nil {
		return errors.New("pipeline: pump is already connected")
	}
	p.down = s
	return nil
}

// Run implements [mrtp.Driver]. It forwards the upstream's io.EOF downstream as
// an end of stream, and reports a downstream error back to a [Failer] upstream.
func (p *Pump[T]) Run(ctx context.Context) error {
	if p.up == nil || p.down == nil {
		return errors.New("pipeline: pump runs with both ends wired")
	}
	for {
		packet, err := p.up.Pull(ctx)
		if errors.Is(err, io.EOF) {
			return p.down.EndOfStream()
		}
		if err != nil {
			return err
		}
		if err := p.down.Write(packet); err != nil {
			if f, ok := p.up.(Failer); ok {
				f.Fail(err)
			}
			return err
		}
	}
}

// Close implements mrtp.Element.
func (p *Pump[T]) Close() error {
	return nil
}

var (
	_ mrtp.Consumer[int] = (*Pump[int])(nil)
	_ mrtp.Source[int]   = (*Pump[int])(nil)
	_ mrtp.Driver        = (*Pump[int])(nil)
)
