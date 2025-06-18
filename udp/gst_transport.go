package udp

import (
	"github.com/go-gst/go-gst/gst"
	"github.com/mengelbart/mrtp/gstreamer"
)

type ID int
type PortNumber int

type GSTTransport struct {
	bin     *gst.Bin
	sinks   map[ID]*gst.Element
	sources map[ID]*gst.Element
}

func NewGSTTransport(address string, sinks map[ID]PortNumber, sources map[ID]PortNumber) (*GSTTransport, error) {
	bin := gst.NewBin("udp-transport")
	t := &GSTTransport{
		bin:     bin,
		sinks:   map[ID]*gst.Element{},
		sources: map[ID]*gst.Element{},
	}
	for id, port := range sinks {
		e, err := makeUDPSinkElement(address, port)
		if err != nil {
			return nil, err
		}
		t.sinks[id] = e
	}
	for id, port := range sources {
		e, err := makeUDPSourceElement(address, port)
		if err != nil {
			return nil, err
		}
		t.sources[id] = e
	}

	return t, nil
}

func (t *GSTTransport) GetSink(id int) *gst.Element {
	return t.sinks[ID(id)]
}

func (t *GSTTransport) GetSrc(id int) *gst.Element {
	return t.sources[ID(id)]
}

func makeUDPSinkElement(address string, port PortNumber) (*gst.Element, error) {
	udpsink, err := gst.NewElement("udpsink")
	if err != nil {
		return nil, err
	}
	if err = gstreamer.SetProperties(udpsink, map[string]any{
		"async": false,
		"sync":  false,
		"host":  address,
		"port":  port,
	}); err != nil {
		return nil, err
	}
	return udpsink, nil
}

func makeUDPSourceElement(address string, port PortNumber) (*gst.Element, error) {
	udpsrc, err := gst.NewElement("udpsrc")
	if err != nil {
		return nil, err
	}
	if err = gstreamer.SetProperties(udpsrc, map[string]any{
		"address": address,
		"port":    port,
	}); err != nil {
		return nil, err
	}
	return udpsrc, nil
}
