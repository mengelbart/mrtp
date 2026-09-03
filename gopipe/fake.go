package gopipe

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mengelbart/mrtp"
	"github.com/mengelbart/mrtp/pipeline"
)

// DiscardSink drops everything it is given.
type DiscardSink[T any] struct{}

func NewDiscardSink[T any]() *DiscardSink[T] {
	return &DiscardSink[T]{}
}

// Negotiate implements mrtp.Sink.
func (s *DiscardSink[T]) Negotiate(mrtp.Format) error {
	return nil
}

// Write implements mrtp.Sink.
func (s *DiscardSink[T]) Write(p mrtp.Packet[T]) error {
	p.Release()
	return nil
}

// EndOfStream implements mrtp.Sink.
func (s *DiscardSink[T]) EndOfStream() error {
	return nil
}

// Close implements mrtp.Element.
func (s *DiscardSink[T]) Close() error {
	return nil
}

// fakeWidth, fakeHeight and fakeFPS are the picture the Fake codec claims to
// produce. Its frames carry no media, so only the frame rate has any effect.
const (
	fakeWidth  = 1920
	fakeHeight = 1080
	fakeFPS    = 30
)

// FakeSource produces frames at a constant rate whose sizes exactly match the
// target bitrate.
type FakeSource struct {
	minTargetRateBps uint64
	maxTargetRateBps uint64
	targetBitrateBps atomic.Uint64

	runTime time.Duration
	done    chan struct{}
	stop    sync.Once

	pool *pipeline.Pool[mrtp.EncodedFrame]
	down mrtp.Sink[mrtp.EncodedFrame]
}

// NewFakeSource creates a new FakeSource with the specified target bitrate.
func NewFakeSource(runTime time.Duration, minTargetRateBps, maxTargetRateBps, initTargetBitrateBps uint64) *FakeSource {
	s := &FakeSource{
		minTargetRateBps: minTargetRateBps,
		maxTargetRateBps: maxTargetRateBps,
		runTime:          runTime,
		done:             make(chan struct{}),
		pool: pipeline.NewPool(
			func() *mrtp.EncodedFrame { return &mrtp.EncodedFrame{} },
			func(f *mrtp.EncodedFrame) { f.Data = f.Data[:0] },
		),
	}
	s.targetBitrateBps.Store(initTargetBitrateBps)

	return s
}

// Format implements mrtp.Source.
func (s *FakeSource) Format() mrtp.Format {
	return mrtp.EncodedVideo{
		Codec:  mrtp.Fake,
		Width:  fakeWidth,
		Height: fakeHeight,
	}
}

// FrameDuration is how long one generated frame lasts.
func (s *FakeSource) FrameDuration() time.Duration {
	return time.Second / fakeFPS
}

// Connect implements mrtp.Source.
func (s *FakeSource) Connect(down mrtp.Sink[mrtp.EncodedFrame]) error {
	if s.down != nil {
		return errors.New("gopipe: fake source is already connected")
	}
	s.down = down
	return nil
}

// SetTargetBitrate implements media.Sender. It sets the target bitrate to
// bitrate bits per second.
func (s *FakeSource) SetTargetBitrate(bitrate uint) error {
	// reduce target rate
	decRate := uint64(0.9 * float64(bitrate))
	slog.Info("NEW_TARGET_MEDIA_RATE", "rate", decRate)

	decRate = max(decRate, s.minTargetRateBps)
	decRate = min(decRate, s.maxTargetRateBps)
	s.targetBitrateBps.Store(decRate)
	return nil
}

// Run implements mrtp.Driver. It generates frames at the configured frame rate
// until the run time is up.
func (s *FakeSource) Run(ctx context.Context) error {
	if s.down == nil {
		return errors.New("gopipe: fake source runs with its output wired")
	}
	frameDuration := s.FrameDuration()
	maxFrame := int(s.runTime / frameDuration)
	frameCount := 0

	var pts time.Duration
	ticker := time.NewTicker(frameDuration)
	defer ticker.Stop()

	lastSent := time.Now().UnixMicro()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-s.done:
			return nil

		case <-ticker.C:
			if frameCount >= maxFrame {
				return s.down.EndOfStream()
			}
			frameCount++

			size := int(s.targetBitrateBps.Load()) / (8 * fakeFPS)

			now := time.Now().UnixMicro()
			slog.Info("generate frame", "frame", frameCount, "size", size, "rate (probably)", s.targetBitrateBps.Load(), "time last", now-lastSent)
			lastSent = now

			packet := s.pool.Get()
			value := packet.Value()
			if cap(value.Data) < size {
				value.Data = make([]byte, size)
			} else {
				value.Data = value.Data[:size]
				clear(value.Data)
			}
			value.PTS = pts
			value.Duration = frameDuration
			value.Keyframe = false

			pts += frameDuration

			if err := s.down.Write(packet); err != nil {
				return err
			}
		}
	}
}

// Close implements mrtp.Element.
func (s *FakeSource) Close() error {
	s.stop.Do(func() { close(s.done) })
	return nil
}

var (
	_ mrtp.Source[mrtp.EncodedFrame] = (*FakeSource)(nil)
	_ mrtp.Driver                    = (*FakeSource)(nil)
	_ mrtp.Sink[mrtp.RawFrame]       = (*DiscardSink[mrtp.RawFrame])(nil)
)
