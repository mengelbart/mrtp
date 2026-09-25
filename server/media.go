package server

import (
	"github.com/mengelbart/mrtp"
	"github.com/mengelbart/mrtp/internal/fakemedia"
)

const sendBitrate = 1_000_000

// senderConfig is the media every session sends until it closes.
var senderConfig = fakemedia.SenderConfig{
	FPS:    30,
	MTU:    1200,
	Bounds: mrtp.RateBounds{Initial: sendBitrate, Min: sendBitrate, Max: sendBitrate},
}
