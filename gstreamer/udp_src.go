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

func SetReceiveBufferSize(size int) UDPSrcOption {
	return func(u *UDPSrc) {
		u.recvBufferSize = size
	}
}

type UDPSrc struct {
	element        *gst.Element
	enablePadProbe bool
	recvBufferSize int
}

func NewUDPSrc(address string, port uint32, opts ...UDPSrcOption) (*UDPSrc, error) {
	src := &UDPSrc{
		enablePadProbe: false,
	}
	for _, opt := range opts {
		opt(src)
	}

	element, err := gst.NewElementWithProperties(
		"udpsrc",
		map[string]any{
			"address":     address,
			"port":        port,
			"buffer-size": src.recvBufferSize,
		},
	)
	if err != nil {
		return nil, err
	}
	src.element = element

	if src.enablePadProbe {
		element.GetStaticPad("src").AddProbe(gst.PadProbeTypeBuffer|gst.PadProbeTypeBufferList, getRTPLogPadProbe("UDPSrc"))
	}
	return src, nil
}

func (s *UDPSrc) GetGstElement() *gst.Element {
	return s.element
}
