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
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/time/rate"
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
	defer file.Close()

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
			d.wc.Close()
			return ctx.Err()
		default:
		}

		if d.rateLimiter != nil {
			err := d.rateLimiter.WaitN(ctx, 1024)
			if err != nil {
				d.running.Store(false)
				d.wc.Close()
				return err
			}
		}

		n, readErr := file.Read(buf)
		if n > 0 {
			_, writeErr := d.wc.Write(buf[:n])
			if writeErr != nil {
				d.wc.Close()
				d.running.Store(false)
				return fmt.Errorf("failed to write to sink: %w", writeErr)
			}

			logDataEvent(n)
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

func (d *DataBin) startChunkSource(ctx context.Context) error {
	if d.wc == nil {
		return fmt.Errorf("data sink not set")
	}

	var wg sync.WaitGroup
	wg.Add(15)

	for i := range 15 {
		select {
		case <-ctx.Done():
			d.running.Store(false)
			d.wc.Close()
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}

		go func(chunkNum int) {
			defer wg.Done()
			d.running.Store(true)
			defer d.running.Store(false)

			select {
			case <-ctx.Done():
				return
			default:
			}

			sizeBuf := make([]byte, 8)
			binary.BigEndian.PutUint64(sizeBuf, uint64(100*1000))
			_, err := d.wc.Write(sizeBuf)
			if err != nil {
				slog.Error("DataSrc failed to write size", "error", err, "chunk-number", chunkNum)
				return
			}

			if d.rateLimiter != nil {
				err := d.rateLimiter.WaitN(ctx, 1000)
				if err != nil {
					return
				}
			}

			buf := make([]byte, 1000)

			slog.Info("DataSrc Chunk started", "chunk-number", chunkNum)

			// webrtc dc breaks if we push everything at once
			for range 100 {
				select {
				case <-ctx.Done():
					return
				default:
				}

				n, writeErr := d.wc.Write(buf)
				if writeErr != nil {
					slog.Error("DataSrc failed to write to sink", "error", writeErr, "chunk-number", chunkNum)
					return
				}

				logDataEvent(n)
			}
			slog.Info("DataSrc Chunk finised", "chunk-number", chunkNum)
		}(i)
	}

	// Wait for all goroutines to complete before closing
	wg.Wait()
	d.running.Store(false)
	return d.wc.Close()
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
	rand.Read(buf)

	for {
		select {
		case <-ctx.Done():
			d.running.Store(false)
			d.wc.Close()
			return ctx.Err()
		default:
		}

		if d.rateLimiter != nil {
			err := d.rateLimiter.WaitN(ctx, 1024)
			if err != nil {
				d.running.Store(false)
				d.wc.Close()
				return err
			}
		}

		n, err := d.wc.Write(buf)
		if err != nil {
			d.running.Store(false)
			return err
		}
		logDataEvent(n)
	}
}

func (d *DataBin) Run(ctx context.Context) error {
	if d.useChunkSrc {
		return d.startChunkSource(ctx)
	}

	if d.startDelay > 0 {
		slog.Info("DataBin start delay", "duration", d.startDelay)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(d.startDelay):
		}
	}
	d.running.Store(true)

	if d.useFileSrc {
		return d.startFileSource(ctx)
	}

	return d.startRandomSource(ctx)
}

func logDataEvent(len int) {
	slog.Info("DataSource sent data", "payload-length", len)
}
