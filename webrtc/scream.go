//go:build cgo

package webrtc

import "github.com/mengelbart/mrtp/webrtc/scream"

// EnableSCReAM runs SCReAM congestion control on the local tracks.
func EnableSCReAM(initRate, minRate, maxRate int) Option {
	return func(t *Transport) error {
		factory, err := scream.NewInterceptorFactory(initRate, minRate, maxRate)
		if err != nil {
			return err
		}
		t.registerCCFB()
		t.scream = factory
		t.interceptorRegistry.Add(factory)
		return nil
	}
}
