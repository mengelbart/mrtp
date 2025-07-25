package gstreamer

import (
	"github.com/go-gst/go-gst/gst"
)

type UDPSrcOption func(*UDPSrc)

func EnabelUDPSrcPadProbe(enabled bool) UDPSrcOption {
	return func(u *UDPSrc) {
		u.enablePadProbe = enabled
	}
}

type UDPSrc struct {
	e              *gst.Element
	enablePadProbe bool
}

func NewUDPSrc(address string, port uint32, opts ...UDPSrcOption) (*UDPSrc, error) {
	e, err := gst.NewElementWithProperties(
		"udpsrc",
		map[string]any{
			"address": address,
			"port":    port,
		},
	)
	if err != nil {
		return nil, err
	}
	src := &UDPSrc{
		e:              e,
		enablePadProbe: false,
	}
	for _, opt := range opts {
		opt(src)
	}

	if src.enablePadProbe {
		e.GetStaticPad("src").AddProbe(gst.PadProbeTypeBuffer|gst.PadProbeTypeBufferList, getRTPLogPadProbe("UDPSrc"))
	}
	return &UDPSrc{
		e: e,
	}, nil
}

func (s *UDPSrc) GetGstElement() *gst.Element {
	return s.e
}
