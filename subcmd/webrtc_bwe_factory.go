//go:build cgo

package subcmd

import (
	"fmt"

	"github.com/mengelbart/mrtp/webrtc"
)

type WebRTCBWEFactory interface {
	MakeWebRTCBWE(BWEConfig) ([]webrtc.Option, error)
}

type WebRTCBWEFactoryFunc func(BWEConfig) ([]webrtc.Option, error)

func (f WebRTCBWEFactoryFunc) MakeWebRTCBWE(config BWEConfig) ([]webrtc.Option, error) {
	return f(config)
}

// WebRTCBWEFactories holds the congestion controllers selectable with the
// webrtc -bwe flag. Controllers implementing [mrtp.BWE] are taken from
// [BWEFactories], SCReAM is registered separately because it is an interceptor
// driving its own pacing and target rates.
var WebRTCBWEFactories = map[string]WebRTCBWEFactory{
	"scream": WebRTCBWEFactoryFunc(func(config BWEConfig) ([]webrtc.Option, error) {
		return []webrtc.Option{webrtc.EnableSCReAM(
			int(config.InitTargetRate),
			int(config.MinTargetRate),
			int(config.MaxTargetRate),
		)}, nil
	}),
}

func init() {
	for name, factory := range BWEFactories {
		WebRTCBWEFactories[name] = WebRTCBWEFactoryFunc(func(config BWEConfig) ([]webrtc.Option, error) {
			bwe, err := factory.MakeBWE(config)
			if err != nil {
				return nil, err
			}
			return []webrtc.Option{webrtc.SetBWE(bwe)}, nil
		})
	}
}

func makeWebRTCBWE(name string, config BWEConfig) ([]webrtc.Option, error) {
	factory, ok := WebRTCBWEFactories[name]
	if !ok {
		return nil, fmt.Errorf("unknown BWE: %v", name)
	}
	return factory.MakeWebRTCBWE(config)
}
