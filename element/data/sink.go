package data

import (
	"encoding/binary"
	"fmt"
	"log/slog"

	"github.com/mengelbart/mrtp"
)

// headerSize is the length of the chunk size header the source writes.
const headerSize = 8

// Sink consumes data chunks and counts them off against the chunk sizes the
// source announces. It keeps nothing but the count.
type Sink struct {
	header    [headerSize]byte
	headerLen int

	remaining    uint64
	inChunk      bool
	unbounded    bool
	currentChunk int
}

// NewSink creates a new data sink.
func NewSink() (*Sink, error) {
	return &Sink{}, nil
}

// Negotiate implements mrtp.Sink.
func (d *Sink) Negotiate(f mrtp.Format) error {
	if _, ok := f.(mrtp.Data); !ok {
		return fmt.Errorf("data sink cannot take format %v", f)
	}
	slog.Info("DataSink started")
	return nil
}

// Write implements mrtp.Sink. A chunk boundary may fall anywhere in a packet,
// so the header is collected across packets and the payload is counted off.
func (d *Sink) Write(p mrtp.Packet[mrtp.DataChunk]) error {
	defer p.Release()

	buf := p.Value().Data
	slog.Info("DataSink read", "bytes-read", len(buf))

	for len(buf) > 0 {
		if d.unbounded {
			return nil
		}

		if !d.inChunk {
			n := copy(d.header[d.headerLen:], buf)
			d.headerLen += n
			buf = buf[n:]
			if d.headerLen < headerSize {
				return nil
			}
			d.headerLen = 0
			d.remaining = binary.BigEndian.Uint64(d.header[:])
			d.inChunk = true

			slog.Info("DataSink Chunk started", "chunk-number", d.currentChunk, "chunk-size", d.remaining)

			if d.remaining == 0 { // only one chunk
				slog.Info("DataSink chunksize 0")
				d.unbounded = true
				return nil
			}
			continue
		}

		n := uint64(len(buf))
		if n > d.remaining {
			n = d.remaining
		}
		d.remaining -= n
		buf = buf[n:]
		if d.remaining == 0 {
			slog.Info("DataSink Chunk finished", "chunk-number", d.currentChunk)
			d.currentChunk++
			d.inChunk = false
		}
	}
	return nil
}

// EndOfStream implements mrtp.Sink.
func (d *Sink) EndOfStream() error {
	slog.Info("DataSink finished", "chunks", d.currentChunk)
	return nil
}

// Close implements mrtp.Element.
func (d *Sink) Close() error {
	return nil
}

var _ mrtp.Sink[mrtp.DataChunk] = (*Sink)(nil)
