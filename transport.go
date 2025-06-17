package mrtp

import "github.com/go-gst/go-gst/gst"

type Transport interface {
	GetSink(int) *gst.Element
	GetSrc(int) *gst.Element
}
