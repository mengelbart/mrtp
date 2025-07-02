package gstreamer

import "github.com/go-gst/go-gst/gst"

type UDPSrc struct {
	e *gst.Element
}

func NewUDPSrc(address string, port uint32) (*UDPSrc, error) {
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
	return &UDPSrc{
		e: e,
	}, nil
}

func (s *UDPSrc) GetGstElement() *gst.Element {
	return s.e
}
