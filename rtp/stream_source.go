package rtp

import (
	"errors"
	"fmt"

	"github.com/go-gst/go-gst/gst"
	"github.com/mengelbart/mrtp/gstreamer"
	"github.com/mengelbart/mrtp/media"
)

type Source int

const (
	videotestsrc Source = iota
	filesrc
)

type StreamSourceOption func(*StreamSource) error

type StreamSource struct {
	source             Source
	codec              media.Codec
	fileSourceLocation string
	payloadType        uint

	bin      *gst.Bin
	elements []*gst.Element
	encoder  *gst.Element
}

func StreamSourcePayloadType(pt int) StreamSinkOption {
	return func(rs *StreamSink) error {
		rs.payloadType = pt
		return nil
	}
}

func StreamSourceType(source Source) StreamSourceOption {
	return func(rs *StreamSource) error {
		rs.source = source
		return nil
	}
}

func STreamSourceCodec(codec media.Codec) StreamSourceOption {
	return func(rs *StreamSource) error {
		rs.codec = codec
		return nil
	}
}

func StreamSourceFileSourceLocation(location string) StreamSourceOption {
	return func(rs *StreamSource) error {
		rs.fileSourceLocation = location
		return nil
	}
}

func NewStreamSource(name string, opts ...StreamSourceOption) (*StreamSource, error) {
	s := &StreamSource{
		source:             videotestsrc,
		codec:              media.H264,
		fileSourceLocation: "",
		payloadType:        96,
		bin:                gst.NewBin(name),
		elements:           []*gst.Element{},
		encoder:            &gst.Element{},
	}
	for _, opt := range opts {
		if err := opt(s); err != nil {
			return nil, err
		}
	}

	switch s.source {
	case videotestsrc:
		vts, err := gst.NewElement("videotestsrc")
		if err != nil {
			return nil, err
		}
		s.elements = append(s.elements, vts)
	case filesrc:
		fs, err := gst.NewElement("filesrc")
		if err != nil {
			return nil, err
		}
		s.elements = append(s.elements, fs)
	default:
		return nil, fmt.Errorf("unknown source format: %v", s.source)
	}
	cs, err := gst.NewElement("clocksync")
	if err != nil {
		return nil, err
	}
	s.elements = append(s.elements, cs)

	var pay *gst.Element
	if s.codec == media.H264 {
		s.encoder, err = gst.NewElement("x264enc")
		if err != nil {
			return nil, err
		}
		pay, err = gst.NewElement("rtph264pay")
		if err != nil {
			return nil, err
		}
		if err = gstreamer.SetProperties(pay, map[string]any{
			"pt":            s.payloadType,
			"mtu":           uint(1200),
			"seqnum-offset": 1,
		}); err != nil {
			return nil, err
		}
		s.elements = append(s.elements, s.encoder, pay)
	} else {
		return nil, fmt.Errorf("unknown codec: %v", s.codec)
	}

	if err = s.bin.AddMany(s.elements...); err != nil {
		return nil, err
	}
	if err = gst.ElementLinkMany(s.elements...); err != nil {
		return nil, err
	}

	srcpad := pay.GetStaticPad("src")
	ghostpad := gst.NewGhostPad("src", srcpad)
	if !s.bin.AddPad(ghostpad.Pad) {
		return nil, errors.New("failed to add ghostpad to RTPStreamSource")
	}

	return s, nil
}

func (s *StreamSource) Element() *gst.Element {
	return s.bin.Element
}

func (s *StreamSource) Link(element *gst.Element) error {
	return s.bin.Link(element)
}

func (s *StreamSource) LinkPad(pad *gst.Pad) error {
	pads, err := s.bin.GetSrcPads()
	if err != nil {
		return err
	}
	if len(pads) != 1 {
		return errors.New("source does not have exactly 1 source pad, was it initialized correctly?")
	}
	ret := pads[0].Link(pad)
	if ret != gst.PadLinkOK {
		return fmt.Errorf("failed to link pads: %v", ret)
	}
	return nil
}
