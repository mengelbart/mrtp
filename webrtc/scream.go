//go:build cgo

package webrtc

import "github.com/mengelbart/mrtp/webrtc/internal/scream"

// EnableSCReAM runs SCReAM congestion control on the local tracks.
func EnableSCReAM(initRate, minRate, maxRate int) Option {
	return func(t *Transport) error {
		factory, err := scream.NewInterceptorFactory(initRate, minRate, maxRate)
		if err != nil {
			return err
		}
		if err := t.setRateController(&screamController{factory: factory, transport: t}); err != nil {
			return err
		}
		t.registerCCFB()
		t.interceptorRegistry.Add(factory)
		return nil
	}
}
