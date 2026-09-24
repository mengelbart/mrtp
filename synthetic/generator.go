// Package synthetic generates traffic without real content: payloads of zeros
// or random bytes, paced to a target bitrate. Adapters turn its writes into
// the packets of a pipeline edge.
package synthetic

import (
	"context"
	"crypto/rand"
	"errors"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/mengelbart/mrtp"
)

// Pacing selects how writes are spaced and sized.
type Pacing int

const (
	// Interval emits one write per tick of Config.FrameRate, sized so the
	// writes add up to the target bitrate. A late write is emitted
	// immediately, so the writes keep to an absolute schedule.
	Interval Pacing = iota

	// Size emits writes of Config.Size bytes, spaced by a token bucket at the
	// target bitrate, or back to back if Config.Bounds.Max is 0.
	Size
)

// Burst is an on/off pattern: every Period, Bytes are written with Size
// pacing. Burst k is due at start + (k+1)*Period. A burst that is due while
// the previous one is still being written starts right after it, so a slow
// link builds a backlog instead of skipping bursts.
type Burst struct {
	Bytes  int
	Period time.Duration
	// Count is the number of bursts, 0 for no limit.
	Count int
}

// Config configures a Generator.
type Config struct {
	Pacing Pacing

	// FrameRate is the tick rate of Interval pacing.
	FrameRate mrtp.FrameRate

	// Size is the write size of Size pacing.
	Size int

	// Burst is an optional on/off pattern for Size pacing.
	Burst *Burst

	// Bounds are the initial bitrate and the bounds of every target bitrate,
	// in bits per second. Max is 0 for unlimited Size pacing.
	Bounds mrtp.RateBounds

	// Duration is how long the generator runs, 0 for no limit.
	Duration time.Duration

	// StartDelay is how long the generator waits before its first write.
	StartDelay time.Duration

	// Random fills payloads with random bytes instead of zeros.
	Random bool
}

// Write is one generated payload.
type Write struct {
	// Payload is only valid until the emit callback returns.
	Payload []byte

	// PTS is the write's schedule time relative to the start, and Duration
	// the time to the next scheduled write, 0 if writes are not scheduled.
	PTS      time.Duration
	Duration time.Duration

	// Burst is the index of the burst the write belongs to, -1 without bursts.
	Burst int

	// First marks the first write of a burst, or of the stream without
	// bursts. Last marks the last write of a burst.
	First bool
	Last  bool
}

// Generator produces writes according to its Config.
type Generator struct {
	config  Config
	limiter *Limiter
	bitrate atomic.Uint64
	active  atomic.Bool
	buf     []byte
}

// New validates config and returns a Generator for it.
func New(config Config) (*Generator, error) {
	if config.Bounds.Min > config.Bounds.Max && config.Bounds.Max > 0 {
		return nil, errors.New("synthetic: rate bounds need min <= max")
	}
	g := &Generator{config: config}
	switch config.Pacing {
	case Interval:
		if config.FrameRate.Num <= 0 || config.FrameRate.Den <= 0 {
			return nil, errors.New("synthetic: interval pacing needs a positive frame rate")
		}
		if config.Bounds.Max == 0 {
			return nil, errors.New("synthetic: interval pacing sizes writes from the bitrate, so it needs a maximum bitrate")
		}
		if config.Burst != nil {
			return nil, errors.New("synthetic: bursts need size pacing")
		}
		g.bitrate.Store(uint64(config.Bounds.Clamp(config.Bounds.Initial)))
	case Size:
		if config.Size <= 0 {
			return nil, errors.New("synthetic: size pacing needs a positive write size")
		}
		if b := config.Burst; b != nil && (b.Bytes <= 0 || b.Period <= 0 || b.Count < 0) {
			return nil, errors.New("synthetic: a burst needs positive bytes and period and a count >= 0")
		}
		g.limiter = NewLimiter(config.Bounds, config.Size)
	default:
		return nil, errors.New("synthetic: unknown pacing")
	}
	return g, nil
}

// SetTargetBitrate sets the target bitrate in bits per second, clamped to the
// bounds, and returns the bitrate it set. Unlimited Size pacing ignores it and
// returns 0.
func (g *Generator) SetTargetBitrate(bitrate uint) uint {
	if g.config.Pacing == Size {
		return g.limiter.SetTargetBitrate(bitrate)
	}
	clamped := g.config.Bounds.Clamp(bitrate)
	g.bitrate.Store(uint64(clamped))
	return clamped
}

// Active reports whether the generator is writing: inside a burst with
// bursts, and while Run runs without.
func (g *Generator) Active() bool {
	return g.active.Load()
}

// Run generates writes and hands each one to emit until the configured
// duration or burst count is reached, which returns nil. It returns ctx.Err()
// when ctx is cancelled, and the error of emit when emit fails.
func (g *Generator) Run(ctx context.Context, emit func(Write) error) error {
	if d := g.config.StartDelay; d > 0 {
		slog.Info("synthetic source start delay", "duration", d)
		timer := time.NewTimer(d)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
	defer g.active.Store(false)
	switch {
	case g.config.Pacing == Interval:
		g.active.Store(true)
		return g.runInterval(ctx, emit)
	case g.config.Burst != nil:
		return g.runBursts(ctx, emit)
	default:
		g.active.Store(true)
		return g.runSize(ctx, emit)
	}
}

// tick is the schedule time of interval write i.
func (g *Generator) tick(i uint64) time.Duration {
	r := g.config.FrameRate
	return time.Duration(i * uint64(r.Den) * uint64(time.Second) / uint64(r.Num))
}

func (g *Generator) runInterval(ctx context.Context, emit func(Write) error) error {
	r := g.config.FrameRate
	count := uint64(g.config.Duration) * uint64(r.Num) / (uint64(r.Den) * uint64(time.Second))
	start := time.Now()
	timer := time.NewTimer(0)
	defer timer.Stop()
	for i := uint64(0); g.config.Duration == 0 || i < count; i++ {
		pts, next := g.tick(i), g.tick(i+1)
		if err := sleepUntil(ctx, timer, start.Add(pts)); err != nil {
			return err
		}
		size := int(g.bitrate.Load() * uint64(r.Den) / (8 * uint64(r.Num)))
		err := emit(Write{
			Payload:  g.payload(size),
			PTS:      pts,
			Duration: next - pts,
			Burst:    -1,
			First:    i == 0,
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func (g *Generator) runSize(ctx context.Context, emit func(Write) error) error {
	start := time.Now()
	for first := true; ; first = false {
		if err := g.limiter.Wait(ctx, g.config.Size); err != nil {
			return err
		}
		if g.config.Duration > 0 && time.Since(start) >= g.config.Duration {
			return nil
		}
		err := emit(Write{
			Payload: g.payload(g.config.Size),
			PTS:     time.Since(start),
			Burst:   -1,
			First:   first,
		})
		if err != nil {
			return err
		}
	}
}

func (g *Generator) runBursts(ctx context.Context, emit func(Write) error) error {
	b := g.config.Burst
	start := time.Now()
	timer := time.NewTimer(0)
	defer timer.Stop()
	for k := 0; b.Count == 0 || k < b.Count; k++ {
		due := time.Duration(k+1) * b.Period
		if g.config.Duration > 0 && due > g.config.Duration {
			return nil
		}
		if err := sleepUntil(ctx, timer, start.Add(due)); err != nil {
			return err
		}
		if err := g.writeBurst(ctx, emit, k, due); err != nil {
			return err
		}
	}
	return nil
}

func (g *Generator) writeBurst(ctx context.Context, emit func(Write) error, k int, pts time.Duration) error {
	g.active.Store(true)
	defer g.active.Store(false)

	size := g.config.Size
	for written := 0; written < g.config.Burst.Bytes; written += size {
		n := min(size, g.config.Burst.Bytes-written)
		if err := g.limiter.Wait(ctx, n); err != nil {
			return err
		}
		err := emit(Write{
			Payload: g.payload(n),
			PTS:     pts,
			Burst:   k,
			First:   written == 0,
			Last:    written+n == g.config.Burst.Bytes,
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// payload returns n bytes of zeros, or of random bytes if configured.
func (g *Generator) payload(n int) []byte {
	if cap(g.buf) < n {
		g.buf = make([]byte, n)
	}
	buf := g.buf[:n]
	if g.config.Random {
		_, _ = rand.Read(buf)
	}
	return buf
}

// sleepUntil waits for t or for ctx, whichever comes first.
func sleepUntil(ctx context.Context, timer *time.Timer, t time.Time) error {
	timer.Reset(time.Until(t))
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
