package gopipe

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/pion/logging"
)

type FakeSink struct {
}

func NewFakeSink() (*Y4MSink, error) {
	return &Y4MSink{}, nil
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

	minTargetRateBps int
	maxTargetRateBps int
	targetBitrateBps int
	fps              int
	bitrateUpdateCh  chan int

	done chan struct{}
	wg   sync.WaitGroup

	runTime time.Duration
}

// NewFakeSource creates a new FakeSource with the specified target bitrate.
func NewFakeSource(runTime time.Duration, minTargetRateBps, maxTargetRateBps, initTargetBitrateBps int) *FakeSource {
	return &FakeSource{
		logger:           logging.NewDefaultLoggerFactory().NewLogger("perfect_codec"),
		minTargetRateBps: minTargetRateBps,
		maxTargetRateBps: maxTargetRateBps,
		targetBitrateBps: initTargetBitrateBps,
		fps:              30,
		bitrateUpdateCh:  make(chan int),
		done:             make(chan struct{}),
		wg:               sync.WaitGroup{},
		runTime:          runTime,
	}
}

func (s *FakeSource) GetInfo() Info {
	return Info{
		Width:       1920,
		Height:      1080,
		TimebaseNum: 30,
		TimebaseDen: 1,
	}
}

// setTargetBitrate sets the target bitrate to r bits per second.
func (c *FakeSource) SetTargetRate(targetRate uint64) {
	// reduce target rate
	targetRate = uint64(0.9 * float64(targetRate))
	slog.Info("NEW_TARGET_MEDIA_RATE", "rate", targetRate)

	c.wg.Go(func() {
		select {
		case c.bitrateUpdateCh <- int(targetRate):
		case <-c.done:
		}
	})
}

// Start begins the codec operation, generating frames at the configured frame rate.
func (c *FakeSource) StartLive(ctx context.Context, pipeline Sink) error {
	fps := float64(30) / float64(1)
	msToNextFrame := time.Duration(float64(time.Second) / fps)

	maxFrame := c.runTime / msToNextFrame
	FrameCount := 0

	pts := int64(0)
	ticker := time.NewTicker(msToNextFrame)

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

			size := c.targetBitrateBps / (8.0 * c.fps)
			buf := make([]byte, size)

			attr := Attributes{}
			attr[PTS] = pts
			attr[FrameDuration] = msToNextFrame

			pts += msToNextFrame.Microseconds()

			err := pipeline.Write(buf, attr)
			if err != nil {
				return err
			}

		case nextRate := <-c.bitrateUpdateCh:
			nextRate = max(nextRate, c.minTargetRateBps)
			nextRate = min(nextRate, c.maxTargetRateBps)
			c.targetBitrateBps = nextRate
		case <-c.done:
			return nil
		}
	}
}

// Close stops the codec and cleans up resources.
func (c *FakeSource) Close() error {
	close(c.done)
	c.wg.Wait()

	return nil
}
