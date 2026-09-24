// Package fake generates encoded frames of the Fake codec, whose sizes follow
// a target bitrate and whose payloads are zeros.
package fake

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/mengelbart/mrtp"
	"github.com/mengelbart/mrtp/media"
	"github.com/mengelbart/mrtp/pipeline"
)

// width and height are the picture the Fake codec claims to produce. Its
// frames carry no media, so they have no effect.
const (
	width  = 1920
	height = 1080
)

// Source emits a frame of bitrate/(8*fps) bytes every 1/fps seconds, for
// duration. Frame i is due at start + i/fps and a late frame is emitted
// immediately.
type Source struct {
	fps      uint64
	duration time.Duration
	bounds   media.RateBounds
	bitrate  atomic.Uint64

	pool *pipeline.Pool[mrtp.EncodedFrame]
	down mrtp.Sink[mrtp.EncodedFrame]
}

// New returns a Source that starts at bounds.Initial bits per second and
// keeps every target bitrate within bounds.Min and bounds.Max.
func New(duration time.Duration, fps uint64, bounds media.RateBounds) (*Source, error) {
	if fps == 0 {
		return nil, errors.New("fake: fps must be positive")
	}
	if bounds.Max == 0 || bounds.Min > bounds.Max {
		return nil, errors.New("fake: rate bounds need 0 <= min <= max and max > 0")
	}
	s := &Source{
		fps:      fps,
		duration: duration,
		bounds:   bounds,
		pool: pipeline.NewPool(
			func() *mrtp.EncodedFrame { return &mrtp.EncodedFrame{} },
			func(f *mrtp.EncodedFrame) { f.Data = f.Data[:0] },
		),
	}
	s.bitrate.Store(uint64(s.clamp(bounds.Initial)))
	return s, nil
}

// Format implements mrtp.Source.
func (s *Source) Format() mrtp.Format {
	return mrtp.EncodedVideo{Codec: mrtp.Fake, Width: width, Height: height}
}

// FrameDuration is how long one frame lasts.
func (s *Source) FrameDuration() time.Duration {
	return time.Second / time.Duration(s.fps)
}

// Connect implements mrtp.Source.
func (s *Source) Connect(down mrtp.Sink[mrtp.EncodedFrame]) error {
	if s.down != nil {
		return errors.New("fake: source is already connected")
	}
	s.down = down
	return nil
}

// SetTargetBitrate implements media.Sender. The bitrate is clamped to the
// source's rate bounds.
func (s *Source) SetTargetBitrate(bitrate uint) error {
	rate := s.clamp(bitrate)
	slog.Info("NEW_TARGET_MEDIA_RATE", "rate", rate)
	s.bitrate.Store(uint64(rate))
	return nil
}

func (s *Source) clamp(bitrate uint) uint {
	return min(max(bitrate, s.bounds.Min), s.bounds.Max)
}

// Run implements mrtp.Driver.
func (s *Source) Run(ctx context.Context) error {
	if s.down == nil {
		return errors.New("fake: source runs with its output wired")
	}
	frames := uint64(s.duration) * s.fps / uint64(time.Second)
	start := time.Now()
	timer := time.NewTimer(0)
	defer timer.Stop()
	var pts time.Duration
	for i := range frames {
		next := time.Duration((i + 1) * uint64(time.Second) / s.fps)
		timer.Reset(time.Until(start.Add(pts)))
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}

		packet := s.pool.Get()
		frame := packet.Value()
		size := int(s.bitrate.Load() / (8 * s.fps))
		if cap(frame.Data) < size {
			frame.Data = make([]byte, size)
		} else {
			frame.Data = frame.Data[:size]
			clear(frame.Data)
		}
		frame.PTS = pts
		frame.Duration = next - pts
		frame.Keyframe = false
		if err := s.down.Write(packet); err != nil {
			return err
		}
		pts = next
	}
	return s.down.EndOfStream()
}

// Close implements mrtp.Element.
func (s *Source) Close() error {
	return nil
}

var (
	_ mrtp.Source[mrtp.EncodedFrame] = (*Source)(nil)
	_ mrtp.Driver                    = (*Source)(nil)
	_ media.Sender                   = (*Source)(nil)
)
