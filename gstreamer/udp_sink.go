package gstreamer

import (
	"github.com/go-gst/go-gst/gst"
)

type UDPSinkOption func(*UDPSink)

func EnabelUDPSinkPadProbe(enabled bool) UDPSinkOption {
	return func(u *UDPSink) {
		u.enablePadProbe = enabled
	}
}

type UDPSink struct {
	e              *gst.Element
	enablePadProbe bool
}

func NewUDPSink(address string, port uint32, opts ...UDPSinkOption) (*UDPSink, error) {
	e, err := gst.NewElementWithProperties(
		"udpsink",
		map[string]any{
			"async": false,
			"sync":  false,
			"host":  address,
			"port":  int(port),
		},
	)
	if err != nil {
		return nil, err
	}
	sink := &UDPSink{
		e: e,
	}
	for _, opt := range opts {
		opt(sink)
	}

	if sink.enablePadProbe {
		e.GetStaticPad("sink").AddProbe(gst.PadProbeTypeBuffer, getRTPLogPadProbe("UDPSink"))
	}

	return sink, nil
}

func (s *UDPSink) GetGstElement() *gst.Element {
	return s.e
}
