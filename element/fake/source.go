// Package fake generates encoded frames of the Fake codec, whose sizes follow
// a target bitrate and whose payloads are zeros.
package fake

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/mengelbart/mrtp"
	"github.com/mengelbart/mrtp/pipeline"
	"github.com/mengelbart/mrtp/synthetic"
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
	gen       *synthetic.Generator
	frameRate mrtp.FrameRate

	pool *pipeline.Pool[mrtp.EncodedFrame]
	down mrtp.Sink[mrtp.EncodedFrame]
}

// New returns a Source that starts at bounds.Initial bits per second and
// keeps every target bitrate within bounds.Min and bounds.Max.
func New(duration time.Duration, fps uint64, bounds mrtp.RateBounds) (*Source, error) {
	frameRate := mrtp.FrameRate{Num: int(fps), Den: 1}
	gen, err := synthetic.New(synthetic.Config{
		Pacing:    synthetic.Interval,
		FrameRate: frameRate,
		Bounds:    bounds,
		Duration:  duration,
	})
	if err != nil {
		return nil, err
	}
	return &Source{
		gen:       gen,
		frameRate: frameRate,
		pool: pipeline.NewPool(
			func() *mrtp.EncodedFrame { return &mrtp.EncodedFrame{} },
			func(f *mrtp.EncodedFrame) { f.Data = f.Data[:0] },
		),
	}, nil
}

// Format implements mrtp.Source.
func (s *Source) Format() mrtp.Format {
	return mrtp.EncodedVideo{Codec: mrtp.Fake, Width: width, Height: height}
}

// FrameDuration is how long one frame lasts.
func (s *Source) FrameDuration() time.Duration {
	return s.frameRate.Duration()
}

// Connect implements mrtp.Source.
func (s *Source) Connect(down mrtp.Sink[mrtp.EncodedFrame]) error {
	if s.down != nil {
		return errors.New("fake: source is already connected")
	}
	s.down = down
	return nil
}

// SetTargetBitrate implements mrtp.TargetBitrateSetter. The bitrate is clamped to the
// source's rate bounds.
func (s *Source) SetTargetBitrate(bitrate uint) error {
	slog.Info("NEW_TARGET_MEDIA_RATE", "rate", s.gen.SetTargetBitrate(bitrate))
	return nil
}

// Run implements mrtp.Driver.
func (s *Source) Run(ctx context.Context) error {
	if s.down == nil {
		return errors.New("fake: source runs with its output wired")
	}
	err := s.gen.Run(ctx, func(w synthetic.Write) error {
		packet := s.pool.Get()
		frame := packet.Value()
		frame.Data = append(frame.Data[:0], w.Payload...)
		frame.PTS = w.PTS
		frame.Duration = w.Duration
		frame.Keyframe = false
		return s.down.Write(packet)
	})
	if err != nil {
		return err
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
	_ mrtp.TargetBitrateSetter       = (*Source)(nil)
)
