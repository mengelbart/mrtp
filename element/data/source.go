// Package data carries non media payload as a pipeline: sources that produce
// chunks and a sink that consumes them, all over [mrtp.DataChunk].
//
// A source frames its stream for the sink: every chunk of data starts with an
// 8 byte big endian size header, where size 0 announces one chunk without end.
package data

import (
	"context"
	"encoding/binary"
	"errors"
	"log/slog"
	"time"

	"github.com/mengelbart/mrtp"
	"github.com/mengelbart/mrtp/pipeline"
	"github.com/mengelbart/mrtp/synthetic"
)

// StreamConfig is one chunk without end, written in 1 KiB pieces.
func StreamConfig() synthetic.Config {
	return synthetic.Config{
		Pacing: synthetic.Size,
		Size:   1024,
	}
}

// ChunkConfig emulates an application that periodically sends small files:
// 15 chunks of 100 kB, one every 5 seconds, written in 1000 byte pieces.
func ChunkConfig() synthetic.Config {
	return synthetic.Config{
		Pacing: synthetic.Size,
		Size:   1000,
		Burst: &synthetic.Burst{
			Bytes:  100_000,
			Period: 5 * time.Second,
			Count:  15,
		},
	}
}

// Source produces synthetic data chunks. Each burst of its generator is one
// chunk, and a generator without bursts is one chunk without end. It is an
// [mrtp.Source] and the driver of its segment.
type Source struct {
	gen        *synthetic.Generator
	chunkBytes uint64

	down mrtp.Sink[mrtp.DataChunk]
	pool *pipeline.Pool[mrtp.DataChunk]
}

// NewSource creates a data source that generates the traffic config describes.
// It needs size pacing.
func NewSource(config synthetic.Config) (*Source, error) {
	if config.Pacing != synthetic.Size {
		return nil, errors.New("data: source needs size pacing")
	}
	gen, err := synthetic.New(config)
	if err != nil {
		return nil, err
	}
	s := &Source{
		gen:  gen,
		pool: newPool(config.Size),
	}
	if config.Burst != nil {
		s.chunkBytes = uint64(config.Burst.Bytes)
	}
	return s, nil
}

// Format implements mrtp.Source.
func (s *Source) Format() mrtp.Format {
	return mrtp.Data{}
}

// Connect implements mrtp.Source.
func (s *Source) Connect(down mrtp.Sink[mrtp.DataChunk]) error {
	if s.down != nil {
		return errors.New("data: source is already connected")
	}
	s.down = down
	return nil
}

// Close implements mrtp.Element.
func (s *Source) Close() error {
	return nil
}

// Running reports whether the source is sending a chunk.
func (s *Source) Running() bool {
	return s.gen.Active()
}

// SetTargetBitrate implements mrtp.TargetBitrateSetter.
func (s *Source) SetTargetBitrate(bitrate uint) error {
	slog.Info("NEW_TARGET_DATA_RATE", "rate", s.gen.SetTargetBitrate(bitrate))
	return nil
}

// Run implements mrtp.Driver.
func (s *Source) Run(ctx context.Context) error {
	if s.down == nil {
		return errors.New("data: source runs with its output wired")
	}
	chunk := 0
	err := s.gen.Run(ctx, func(w synthetic.Write) error {
		if w.First {
			slog.Info("DataSrc Chunk started", "chunk-number", chunk)
		}
		if err := writeChunk(s.down, s.pool, w.Payload, w.First, s.chunkBytes); err != nil {
			return err
		}
		if w.Last {
			slog.Info("DataSrc Chunk finished", "chunk-number", chunk)
			chunk++
		}
		return nil
	})
	if err != nil {
		return err
	}
	return s.down.EndOfStream()
}

// newPool returns a pool of chunks with room for a header and size bytes.
func newPool(size int) *pipeline.Pool[mrtp.DataChunk] {
	return pipeline.NewPool(
		func() *mrtp.DataChunk {
			return &mrtp.DataChunk{Data: make([]byte, 0, headerSize+size)}
		},
		func(c *mrtp.DataChunk) {
			c.Data = c.Data[:0]
		},
	)
}

// writeChunk sends payload downstream, preceded by a size header if header is
// set. The packet carries a copy, so the caller keeps its buffer.
func writeChunk(down mrtp.Sink[mrtp.DataChunk], pool *pipeline.Pool[mrtp.DataChunk], payload []byte, header bool, size uint64) error {
	packet := pool.Get()
	chunk := packet.Value()
	chunk.Data = chunk.Data[:0]
	if header {
		chunk.Data = binary.BigEndian.AppendUint64(chunk.Data, size)
	}
	chunk.Data = append(chunk.Data, payload...)
	return down.Write(packet)
}

var (
	_ mrtp.Source[mrtp.DataChunk] = (*Source)(nil)
	_ mrtp.Driver                 = (*Source)(nil)
	_ mrtp.TargetBitrateSetter    = (*Source)(nil)
)
