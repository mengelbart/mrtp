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

	"golang.org/x/time/rate"
)

const (
	chunkCount     = 15
	chunkInterval  = 5 * time.Second
	chunkWriteSize = 1000
	chunkWrites    = 100
	chunkSize      = chunkWriteSize * chunkWrites
)

type DataBinOption func(*DataBin) error

type DataBin struct {
	useFileSrc  bool
	useChunkSrc bool
	filepath    string

	wc          io.WriteCloser
	rateLimiter *rate.Limiter

	running    atomic.Bool
	startDelay time.Duration
}

func UseFileSource(filepath string) DataBinOption {
	return func(d *DataBin) error {
		d.useFileSrc = true
		d.filepath = filepath
		return nil
	}
}

func UseChunkSource() DataBinOption {
	return func(d *DataBin) error {
		d.useChunkSrc = true
		return nil
	}
}

func SetStartDelay(startDelay time.Duration) DataBinOption {
	return func(d *DataBin) error {
		d.startDelay = startDelay
		return nil
	}
}

// UseRateLimiter: initLimit in bps, burst in bytes
func UseRateLimiter(initLimit, burst uint) DataBinOption {
	return func(d *DataBin) error {
		initLimitToBytes := bitRateToBytesPerSec(initLimit)

		d.rateLimiter = rate.NewLimiter(rate.Limit(initLimitToBytes), int(burst))
		return nil
	}
}

// NewDataBin creates a new data source. wc is the WriteCloser where data will be written to.
func NewDataBin(wc io.WriteCloser, options ...DataBinOption) (*DataBin, error) {
	d := &DataBin{
		useFileSrc: false,
		filepath:   "",
		wc:         wc,
	}
	for _, opt := range options {
		if err := opt(d); err != nil {
			return nil, err
		}
	}
	return d, nil
}

func (d *DataBin) Running() bool {
	return d.running.Load()
}

func (d *DataBin) SetRateLimit(ratebps uint) {
	if d.rateLimiter != nil {
		slog.Info("NEW_TARGET_DATA_RATE", "rate", ratebps)

		rateBytes := bitRateToBytesPerSec(ratebps)
		d.rateLimiter.SetLimit(rate.Limit(rateBytes))
	}
}

func bitRateToBytesPerSec(bitrate uint) float64 {
	return math.Max(float64(bitrate)/8.0, 1)
}
func (d *DataBin) startFileSource(ctx context.Context) error {
	if d.wc == nil {
		return fmt.Errorf("data sink not set")
	}

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

	// write size on channel
	sizeBuf := make([]byte, 8)
	binary.BigEndian.PutUint64(sizeBuf, uint64(fileSize))
	_, err = d.wc.Write(sizeBuf)
	if err != nil {
		return err
	}
	slog.Info("DataSrc Chunk started", "chunk-number", 0)

	buf := make([]byte, 1024)
	for {
		select {
		case <-ctx.Done():
			d.running.Store(false)
			if closeErr := d.wc.Close(); closeErr != nil {
				slog.Error("failed to close writer", "error", closeErr)
			}
			return ctx.Err()
		default:
		}

		if d.rateLimiter != nil {
			waitErr := d.rateLimiter.WaitN(ctx, 1024)
			if waitErr != nil {
				d.running.Store(false)
				return waitErr
			}
		}

		n, readErr := file.Read(buf)
		if n > 0 {
			_, writeErr := d.wc.Write(buf[:n])
			if writeErr != nil {
				if closeErr := d.wc.Close(); closeErr != nil {
					slog.Error("failed to close writer", "error", closeErr)
				}
				d.running.Store(false)
				return fmt.Errorf("failed to write to sink: %w", writeErr)
			}
		}
		if readErr == io.EOF {
			d.running.Store(false)
			return d.wc.Close()
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
// goroutine, which is the only writer to d.wc, so the framing the sink reads stays intact.
func (d *DataBin) startChunkSource(ctx context.Context) error {
	if d.wc == nil {
		return fmt.Errorf("data sink not set")
	}

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
			if closeErr := d.wc.Close(); closeErr != nil {
				slog.Error("failed to close writer", "error", closeErr)
			}
			return err
		}
	}

	d.running.Store(false)
	if err := ctx.Err(); err != nil {
		if closeErr := d.wc.Close(); closeErr != nil {
			slog.Error("failed to close writer", "error", closeErr)
		}
		return err
	}
	return d.wc.Close()
}

func (d *DataBin) writeChunk(ctx context.Context, chunkNum int) error {
	d.running.Store(true)
	defer d.running.Store(false)

	sizeBuf := make([]byte, 8)
	binary.BigEndian.PutUint64(sizeBuf, uint64(chunkSize))
	if _, err := d.wc.Write(sizeBuf); err != nil {
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

		if _, err := d.wc.Write(buf); err != nil {
			return fmt.Errorf("failed to write to sink: %w", err)
		}
	}
	slog.Info("DataSrc Chunk finished", "chunk-number", chunkNum)
	return nil
}

func (d *DataBin) startRandomSource(ctx context.Context) error {
	if d.wc == nil {
		return fmt.Errorf("data sink not set")
	}

	// write size on channel. size = 0 only one chunk
	sizeBuf := make([]byte, 8)
	binary.BigEndian.PutUint64(sizeBuf, uint64(0))
	_, err := d.wc.Write(sizeBuf)
	if err != nil {
		return err
	}

	buf := make([]byte, 1024)

	for {
		select {
		case <-ctx.Done():
			d.running.Store(false)
			if closeErr := d.wc.Close(); closeErr != nil {
				slog.Error("failed to close writer", "error", closeErr)
			}
			return ctx.Err()
		default:
		}

		if d.rateLimiter != nil {
			err := d.rateLimiter.WaitN(ctx, 1024)
			if err != nil {
				d.running.Store(false)
				if closeErr := d.wc.Close(); closeErr != nil {
					slog.Error("failed to close writer", "error", closeErr)
				}
				return err
			}
		}
		rand.Read(buf)

		_, err := d.wc.Write(buf)
		if err != nil {
			d.running.Store(false)
			return err
		}
	}
}

func (d *DataBin) Run(ctx context.Context) error {
	if d.startDelay > 0 {
		slog.Info("DataBin start delay", "duration", d.startDelay)
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
