package data

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync/atomic"
	"time"

	"github.com/mengelbart/mrtp"
	"github.com/mengelbart/mrtp/pipeline"
	"github.com/mengelbart/mrtp/synthetic"
)

// readSize is how much of the file goes into one chunk.
const readSize = 1024

// FileSource sends a file as one chunk, paced to a target bitrate. It is an
// [mrtp.Source] and the driver of its segment.
type FileSource struct {
	path       string
	startDelay time.Duration
	limiter    *synthetic.Limiter
	running    atomic.Bool

	down mrtp.Sink[mrtp.DataChunk]
	pool *pipeline.Pool[mrtp.DataChunk]
}

// NewFileSource creates a source for the file at path. It paces to bounds, or
// is unlimited if bounds.Max is 0, and starts after startDelay.
func NewFileSource(path string, bounds mrtp.RateBounds, startDelay time.Duration) (*FileSource, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("data: file source: %w", err)
	}
	return &FileSource{
		path:       path,
		startDelay: startDelay,
		limiter:    synthetic.NewLimiter(bounds, readSize),
		pool:       newPool(readSize),
	}, nil
}

// Format implements mrtp.Source.
func (s *FileSource) Format() mrtp.Format {
	return mrtp.Data{}
}

// Connect implements mrtp.Source.
func (s *FileSource) Connect(down mrtp.Sink[mrtp.DataChunk]) error {
	if s.down != nil {
		return errors.New("data: file source is already connected")
	}
	s.down = down
	return nil
}

// Close implements mrtp.Element.
func (s *FileSource) Close() error {
	return nil
}

// Running reports whether the source is sending the file.
func (s *FileSource) Running() bool {
	return s.running.Load()
}

// SetTargetBitrate implements mrtp.TargetBitrateSetter.
func (s *FileSource) SetTargetBitrate(bitrate uint) error {
	slog.Info("NEW_TARGET_DATA_RATE", "rate", s.limiter.SetTargetBitrate(bitrate))
	return nil
}

// Run implements mrtp.Driver.
func (s *FileSource) Run(ctx context.Context) error {
	if s.down == nil {
		return errors.New("data: file source runs with its output wired")
	}
	if s.startDelay > 0 {
		slog.Info("data source start delay", "duration", s.startDelay)
		timer := time.NewTimer(s.startDelay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}

	file, err := os.Open(s.path)
	if err != nil {
		return fmt.Errorf("data: failed to open file: %w", err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			slog.Error("failed to close file", "error", closeErr)
		}
	}()
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("data: failed to get file info: %w", err)
	}

	s.running.Store(true)
	defer s.running.Store(false)

	slog.Info("DataSrc Chunk started", "chunk-number", 0)
	buf := make([]byte, readSize)
	for first := true; ; first = false {
		if err := s.limiter.Wait(ctx, readSize); err != nil {
			return err
		}
		n, readErr := file.Read(buf)
		if n > 0 || first {
			if err := writeChunk(s.down, s.pool, buf[:n], first, uint64(info.Size())); err != nil {
				return err
			}
		}
		if errors.Is(readErr, io.EOF) {
			slog.Info("DataSrc Chunk finished", "chunk-number", 0)
			return s.down.EndOfStream()
		}
		if readErr != nil {
			return fmt.Errorf("data: failed to read file: %w", readErr)
		}
	}
}

var (
	_ mrtp.Source[mrtp.DataChunk] = (*FileSource)(nil)
	_ mrtp.Driver                 = (*FileSource)(nil)
	_ mrtp.TargetBitrateSetter    = (*FileSource)(nil)
)
