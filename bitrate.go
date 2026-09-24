package mrtp

// RateBounds are the congestion controller's bitrate bounds, in bits per
// second.
type RateBounds struct {
	Initial uint
	Min     uint
	Max     uint
}

// Clamp limits bitrate to [Min, Max].
func (b RateBounds) Clamp(bitrate uint) uint {
	return min(max(bitrate, b.Min), b.Max)
}

// TargetBitrateSetter is a source whose bitrate rate control steers.
type TargetBitrateSetter interface {
	// SetTargetBitrate sets the target bitrate in bits per second. A source
	// that cannot adapt does nothing and returns nil.
	SetTargetBitrate(uint) error
}
