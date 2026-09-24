package synthetic

import (
	"context"
	"math"

	"github.com/mengelbart/mrtp/media"
	"golang.org/x/time/rate"
)

// Limiter paces writes to a target bitrate with a token bucket. A nil Limiter
// is unlimited.
type Limiter struct {
	bounds  media.RateBounds
	limiter *rate.Limiter
}

// NewLimiter returns a Limiter that starts at bounds.Initial and admits writes
// of up to burst bytes. It returns nil, an unlimited Limiter, if bounds.Max is 0.
func NewLimiter(bounds media.RateBounds, burst int) *Limiter {
	if bounds.Max == 0 {
		return nil
	}
	return &Limiter{
		bounds:  bounds,
		limiter: rate.NewLimiter(bytesPerSecond(bounds.Clamp(bounds.Initial)), burst),
	}
}

// Wait blocks until n bytes may be written.
func (l *Limiter) Wait(ctx context.Context, n int) error {
	if l == nil {
		return ctx.Err()
	}
	return l.limiter.WaitN(ctx, n)
}

// SetTargetBitrate sets the rate in bits per second, clamped to the bounds,
// and returns the rate it set. An unlimited Limiter ignores it and returns 0.
func (l *Limiter) SetTargetBitrate(bitrate uint) uint {
	if l == nil {
		return 0
	}
	clamped := l.bounds.Clamp(bitrate)
	l.limiter.SetLimit(bytesPerSecond(clamped))
	return clamped
}

// bytesPerSecond converts a bitrate to a limit that is never 0, because a
// limiter with limit 0 blocks forever.
func bytesPerSecond(bitrate uint) rate.Limit {
	return rate.Limit(math.Max(float64(bitrate)/8, 1))
}
