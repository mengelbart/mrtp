package datachannels

import (
	"context"
	"errors"
	"io"
	"sync"

	"github.com/mengelbart/mrtp"
	"github.com/mengelbart/mrtp/pipeline"
	"github.com/mengelbart/quicdc"
)

// chunkBufferSize is how much of a message goes into one chunk.
const chunkBufferSize = 2048

// Receiver reads a data channel message and pushes it downstream in chunks. It
// is the driver of its segment.
type Receiver struct {
	dc *quicdc.DataChannel

	rm *quicdc.DataChannelReadMessage

	down mrtp.Sink[mrtp.DataChunk]
	pool *pipeline.Pool[mrtp.DataChunk]

	closeOnce sync.Once
}

func newReceiver(dc *quicdc.DataChannel) *Receiver {
	return &Receiver{
		dc: dc,
		pool: pipeline.NewPool(
			func() *mrtp.DataChunk {
				return &mrtp.DataChunk{Data: make([]byte, chunkBufferSize)}
			},
			func(c *mrtp.DataChunk) {
				c.Data = c.Data[:cap(c.Data)]
			},
		),
	}
}

// Format implements mrtp.Source.
func (r *Receiver) Format() mrtp.Format {
	return mrtp.Data{}
}

// Connect implements mrtp.Source.
func (r *Receiver) Connect(s mrtp.Sink[mrtp.DataChunk]) error {
	r.down = s
	return nil
}

// Run implements mrtp.Driver, reading the channel until it ends.
func (r *Receiver) Run(ctx context.Context) error {
	if r.rm == nil {
		var err error
		r.rm, err = r.dc.ReceiveMessage(ctx)
		if err != nil {
			return err
		}
	}

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		packet := r.pool.Get()
		chunk := packet.Value()
		n, err := r.rm.Read(chunk.Data)
		if n > 0 {
			chunk.Data = chunk.Data[:n]
			if writeErr := r.down.Write(packet); writeErr != nil {
				return writeErr
			}
		} else {
			packet.Release()
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return r.down.EndOfStream()
			}
			return err
		}
	}
}

// Close closes the pending message and the data channel. Repeated calls are
// no-ops.
func (r *Receiver) Close() error {
	var err error
	r.closeOnce.Do(func() {
		if r.rm != nil {
			err = r.rm.Close()
		}
		if closeErr := r.dc.Close(); err == nil {
			err = closeErr
		}
	})
	return err
}

var (
	_ mrtp.Source[mrtp.DataChunk] = (*Receiver)(nil)
	_ mrtp.Driver                 = (*Receiver)(nil)
)
