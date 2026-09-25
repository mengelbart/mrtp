//go:build cgo

package gstreamer

import (
	"github.com/go-gst/go-gst/gst"
)

// newUDPSinkElement creates a udpsink sending to host:port.
func newUDPSinkElement(host string, port uint, traceRTP bool) (*gst.Element, error) {
	element, err := gst.NewElementWithProperties(
		"udpsink",
		map[string]any{
			"async": false,
			"sync":  false,
			"host":  host,
			"port":  int(port),
		},
	)
	if err != nil {
		return nil, err
	}
	if traceRTP {
		element.GetStaticPad("sink").AddProbe(
			gst.PadProbeTypeBuffer|gst.PadProbeTypeBufferList, getRTPLogPadProbe("UDPSink"))
	}
	return element, nil
}

// newUDPSrcElement creates a udpsrc receiving on host:port. A receive buffer
// size of zero leaves the operating system's default in place.
func newUDPSrcElement(host string, port uint, traceRTP bool, recvBufferSize int) (*gst.Element, error) {
	element, err := gst.NewElementWithProperties(
		"udpsrc",
		map[string]any{
			"address":     host,
			"port":        int(port),
			"buffer-size": recvBufferSize,
		},
	)
	if err != nil {
		return nil, err
	}
	if traceRTP {
		element.GetStaticPad("src").AddProbe(
			gst.PadProbeTypeBuffer|gst.PadProbeTypeBufferList, getRTPLogPadProbe("UDPSrc"))
	}
	return element, nil
}
