package gstreamer

import "github.com/go-gst/go-gst/gst"

type UDPSink struct {
	e *gst.Element
}

func NewUDPSink(address string, port uint32) (*UDPSink, error) {
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

	return &UDPSink{
		e: e,
	}, nil
}

func (s *UDPSink) GetGstElement() *gst.Element {
	return s.e
}
