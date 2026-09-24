//go:build cgo

package subcmd

import (
	"github.com/mengelbart/mrtp/webrtc"
)

// SCReAM is registered separately because it is an interceptor driving its own
// pacing and target rates.
func init() {
	WebRTCBWEFactories["scream"] = WebRTCBWEFactoryFunc(func(config BWEConfig) ([]webrtc.Option, error) {
		return []webrtc.Option{webrtc.EnableSCReAM(
			int(config.InitTargetRate),
			int(config.MinTargetRate),
			int(config.MaxTargetRate),
		)}, nil
	})
}
