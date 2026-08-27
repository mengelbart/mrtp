package gopipe

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/pion/logging"
)

type FakeSink struct {
}

func NewFakeSink() (*FakeSink, error) {
	return &FakeSink{}, nil
}

func (s *FakeSink) Close() error {
	return nil
}

func (a *FakeSink) Write(b []byte, attrs Attributes) error {
	return nil
}

// FakeSource implements a simple codec that produces frames at a constant rate
// with sizes exactly matching the target bitrate.
type FakeSource struct {
	logger logging.LeveledLogger

	minTargetRateBps uint64
	maxTargetRateBps uint64
	targetBitrateBps atomic.Uint64
	fps              int

	done chan struct{}

	runTime time.Duration
}

// NewFakeSource creates a new FakeSource with the specified target bitrate.
func NewFakeSource(runTime time.Duration, minTargetRateBps, maxTargetRateBps, initTargetBitrateBps uint64) *FakeSource {
	fs := &FakeSource{
		logger:           logging.NewDefaultLoggerFactory().NewLogger("perfect_codec"),
		minTargetRateBps: minTargetRateBps,
		maxTargetRateBps: maxTargetRateBps,
		fps:              30,
		done:             make(chan struct{}),
		runTime:          runTime,
	}
	fs.targetBitrateBps.Store(initTargetBitrateBps)

	return fs
}

func (s *FakeSource) GetInfo() Info {
	return Info{
		Width:       1920,
		Height:      1080,
		TimebaseNum: 30,
		TimebaseDen: 1,
	}
}

// SetTargetBitrate implements media.Sender. It sets the target bitrate to
// bitrate bits per second.
func (c *FakeSource) SetTargetBitrate(bitrate uint) error {
	// reduce target rate
	decRate := uint64(0.9 * float64(bitrate))
	slog.Info("NEW_TARGET_MEDIA_RATE", "rate", decRate)

	decRate = max(decRate, c.minTargetRateBps)
	decRate = min(decRate, c.maxTargetRateBps)
	c.targetBitrateBps.Store(decRate)
	return nil
}

// Start begins the codec operation, generating frames at the configured frame rate.
func (c *FakeSource) StartLive(ctx context.Context, pipeline Sink) error {
	fps := float64(30) / float64(1)
	msToNextFrame := time.Duration(float64(time.Second) / fps)

	maxFrame := c.runTime / msToNextFrame
	FrameCount := 0

	pts := int64(0)
	ticker := time.NewTicker(msToNextFrame)

	lastSent := time.Now().UnixMicro()

	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case <-ticker.C:
			if FrameCount >= int(maxFrame) {
				return nil
			}
			FrameCount++

			size := int(c.targetBitrateBps.Load()) / (8.0 * c.fps)
			buf := make([]byte, size)

			now := time.Now().UnixMicro()
			slog.Info("generate frame", "frame", FrameCount, "size", size, "rate (probably)", c.targetBitrateBps.Load(), "time last", now-lastSent)
			lastSent = now

			attr := Attributes{}
			attr[PTS] = pts
			attr[FrameDuration] = msToNextFrame

			pts += msToNextFrame.Microseconds()

			err := pipeline.Write(buf, attr)
			if err != nil {
				return err
			}
		case <-c.done:
			return nil
		}
	}
}

// Close stops the codec and cleans up resources.
func (c *FakeSource) Close() error {
	close(c.done)

	return nil
}
