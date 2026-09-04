// Package data carries non media payload as a pipeline: a source that produces
// chunks and a sink that consumes them, both over [mrtp.DataChunk].
package data

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"sync/atomic"
	"time"

	"github.com/mengelbart/mrtp"
	"github.com/mengelbart/mrtp/pipeline"
	"golang.org/x/time/rate"
)

const (
	chunkCount     = 15
	chunkInterval  = 5 * time.Second
	chunkWriteSize = 1000
	chunkWrites    = 100
	chunkSize      = chunkWriteSize * chunkWrites

	// readSize is how much of a file or of random data goes into one chunk.
	readSize = 1024
)

type Option func(*Source) error

// Source produces data chunks, either from a file, as periodic chunks, or as
// random bytes. It is an [mrtp.Source] and the driver of its segment.
type Source struct {
	useFileSrc  bool
	useChunkSrc bool
	filepath    string

	down mrtp.Sink[mrtp.DataChunk]
	pool *pipeline.Pool[mrtp.DataChunk]

	rateLimiter *rate.Limiter

	running    atomic.Bool
	startDelay time.Duration
}

func UseFileSource(filepath string) Option {
	return func(d *Source) error {
		d.useFileSrc = true
		d.filepath = filepath
		return nil
	}
}

func UseChunkSource() Option {
	return func(d *Source) error {
		d.useChunkSrc = true
		return nil
	}
}

func SetStartDelay(startDelay time.Duration) Option {
	return func(d *Source) error {
		d.startDelay = startDelay
		return nil
	}
}

// UseRateLimiter: initLimit in bps, burst in bytes
func UseRateLimiter(initLimit, burst uint) Option {
	return func(d *Source) error {
		initLimitToBytes := bitRateToBytesPerSec(initLimit)

		d.rateLimiter = rate.NewLimiter(rate.Limit(initLimitToBytes), int(burst))
		return nil
	}
}

// NewSource creates a new data source.
func NewSource(options ...Option) (*Source, error) {
	d := &Source{
		useFileSrc: false,
		filepath:   "",
		pool: pipeline.NewPool(
			func() *mrtp.DataChunk {
				return &mrtp.DataChunk{Data: make([]byte, 0, chunkWriteSize)}
			},
			func(c *mrtp.DataChunk) {
				c.Data = c.Data[:0]
			},
		),
	}
	for _, opt := range options {
		if err := opt(d); err != nil {
			return nil, err
		}
	}
	return d, nil
}

// Format implements mrtp.Source.
func (d *Source) Format() mrtp.Format {
	return mrtp.Data{}
}

// Connect implements mrtp.Source.
func (d *Source) Connect(s mrtp.Sink[mrtp.DataChunk]) error {
	d.down = s
	return nil
}

// Close implements mrtp.Element. The source holds nothing past Run.
func (d *Source) Close() error {
	return nil
}

func (d *Source) Running() bool {
	return d.running.Load()
}

func (d *Source) SetRateLimit(ratebps uint) {
	if d.rateLimiter != nil {
		slog.Info("NEW_TARGET_DATA_RATE", "rate", ratebps)

		rateBytes := bitRateToBytesPerSec(ratebps)
		d.rateLimiter.SetLimit(rate.Limit(rateBytes))
	}
}

func bitRateToBytesPerSec(bitrate uint) float64 {
	return math.Max(float64(bitrate)/8.0, 1)
}

// write sends buf downstream as one chunk. The packet carries a copy, so the
// caller keeps its buffer.
func (d *Source) write(buf []byte) error {
	packet := d.pool.Get()
	chunk := packet.Value()
	chunk.Data = append(chunk.Data[:0], buf...)
	return d.down.Write(packet)
}

// writeSize sends the 8 byte chunk header the sink reads.
func (d *Source) writeSize(size uint64) error {
	sizeBuf := make([]byte, 8)
	binary.BigEndian.PutUint64(sizeBuf, size)
	return d.write(sizeBuf)
}

// end reports the end of the stream downstream.
func (d *Source) end() error {
	return d.down.EndOfStream()
}

func (d *Source) startFileSource(ctx context.Context) error {
	file, err := os.Open(d.filepath)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			slog.Error("failed to close file", "error", closeErr)
		}
	}()

	// Get file size
	fileInfo, err := file.Stat()
	if err != nil {
		return fmt.Errorf("failed to get file info: %w", err)
	}
	fileSize := fileInfo.Size()

	if err = d.writeSize(uint64(fileSize)); err != nil {
		return err
	}
	slog.Info("DataSrc Chunk started", "chunk-number", 0)

	buf := make([]byte, readSize)
	for {
		select {
		case <-ctx.Done():
			d.running.Store(false)
			if endErr := d.end(); endErr != nil {
				slog.Error("failed to end stream", "error", endErr)
			}
			return ctx.Err()
		default:
		}

		if d.rateLimiter != nil {
			waitErr := d.rateLimiter.WaitN(ctx, readSize)
			if waitErr != nil {
				d.running.Store(false)
				return waitErr
			}
		}

		n, readErr := file.Read(buf)
		if n > 0 {
			if writeErr := d.write(buf[:n]); writeErr != nil {
				if endErr := d.end(); endErr != nil {
					slog.Error("failed to end stream", "error", endErr)
				}
				d.running.Store(false)
				return fmt.Errorf("failed to write to sink: %w", writeErr)
			}
		}
		if readErr == io.EOF {
			d.running.Store(false)
			return d.end()
		}
		if readErr != nil {
			d.running.Store(false)
			return fmt.Errorf("failed to read from file: %w", readErr)
		}
	}
}

// startChunkSource emulates an application periodically sending small files. A chunk is
// queued every chunkInterval regardless of how far the previous one got, so a slow link
// builds a backlog instead of skipping chunks. Chunks are written back to back by this
// goroutine, which is the only writer downstream, so the framing the sink reads stays intact.
func (d *Source) startChunkSource(ctx context.Context) error {
	queue := make(chan int, chunkCount)
	go func() {
		defer close(queue)

		ticker := time.NewTicker(chunkInterval)
		defer ticker.Stop()

		for i := range chunkCount {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			queue <- i
		}
	}()

	for chunkNum := range queue {
		if err := d.writeChunk(ctx, chunkNum); err != nil {
			d.running.Store(false)
			if endErr := d.end(); endErr != nil {
				slog.Error("failed to end stream", "error", endErr)
			}
			return err
		}
	}

	d.running.Store(false)
	if err := ctx.Err(); err != nil {
		if endErr := d.end(); endErr != nil {
			slog.Error("failed to end stream", "error", endErr)
		}
		return err
	}
	return d.end()
}

func (d *Source) writeChunk(ctx context.Context, chunkNum int) error {
	d.running.Store(true)
	defer d.running.Store(false)

	if err := d.writeSize(chunkSize); err != nil {
		return fmt.Errorf("failed to write chunk size: %w", err)
	}

	slog.Info("DataSrc Chunk started", "chunk-number", chunkNum)

	buf := make([]byte, chunkWriteSize)

	// webrtc dc breaks if we push everything at once
	for range chunkWrites {
		if d.rateLimiter != nil {
			if err := d.rateLimiter.WaitN(ctx, chunkWriteSize); err != nil {
				return err
			}
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if err := d.write(buf); err != nil {
			return fmt.Errorf("failed to write to sink: %w", err)
		}
	}
	slog.Info("DataSrc Chunk finished", "chunk-number", chunkNum)
	return nil
}

func (d *Source) startRandomSource(ctx context.Context) error {
	// size = 0 only one chunk
	if err := d.writeSize(0); err != nil {
		return err
	}

	buf := make([]byte, readSize)

	for {
		select {
		case <-ctx.Done():
			d.running.Store(false)
			if endErr := d.end(); endErr != nil {
				slog.Error("failed to end stream", "error", endErr)
			}
			return ctx.Err()
		default:
		}

		if d.rateLimiter != nil {
			err := d.rateLimiter.WaitN(ctx, readSize)
			if err != nil {
				d.running.Store(false)
				if endErr := d.end(); endErr != nil {
					slog.Error("failed to end stream", "error", endErr)
				}
				return err
			}
		}
		rand.Read(buf)

		if err := d.write(buf); err != nil {
			d.running.Store(false)
			return err
		}
	}
}

// Run implements mrtp.Driver.
func (d *Source) Run(ctx context.Context) error {
	if d.down == nil {
		return fmt.Errorf("data source is not connected")
	}

	if d.startDelay > 0 {
		slog.Info("data source start delay", "duration", d.startDelay)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(d.startDelay):
		}
	}

	if d.useChunkSrc {
		return d.startChunkSource(ctx)
	}
	d.running.Store(true)

	if d.useFileSrc {
		return d.startFileSource(ctx)
	}

	return d.startRandomSource(ctx)
}

var (
	_ mrtp.Source[mrtp.DataChunk] = (*Source)(nil)
	_ mrtp.Driver                 = (*Source)(nil)
)
