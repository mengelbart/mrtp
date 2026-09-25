package main

import (
	"fmt"

	"github.com/mengelbart/mrtp/webrtc"
)

type webrtcBWEFactory interface {
	MakeWebRTCBWE(bweConfig) ([]webrtc.Option, error)
}

type webrtcBWEFactoryFunc func(bweConfig) ([]webrtc.Option, error)

func (f webrtcBWEFactoryFunc) MakeWebRTCBWE(config bweConfig) ([]webrtc.Option, error) {
	return f(config)
}

// webrtcBWEFactories holds the congestion controllers selectable with the
// webrtc -bwe flag. Controllers implementing [mrtp.BWE] are taken from
// [bweFactories].
var webrtcBWEFactories = map[string]webrtcBWEFactory{}

func init() {
	for name, factory := range bweFactories {
		webrtcBWEFactories[name] = webrtcBWEFactoryFunc(func(config bweConfig) ([]webrtc.Option, error) {
			bwe, err := factory.MakeBWE(config)
			if err != nil {
				return nil, err
			}
			return []webrtc.Option{webrtc.SetBWE(bwe)}, nil
		})
	}
}

func makeWebRTCBWE(name string, config bweConfig) ([]webrtc.Option, error) {
	factory, ok := webrtcBWEFactories[name]
	if !ok {
		return nil, fmt.Errorf("unknown BWE: %v", name)
	}
	return factory.MakeWebRTCBWE(config)
}
