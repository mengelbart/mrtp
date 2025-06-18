package gstreamer

import (
	"errors"
	"fmt"

	"github.com/go-gst/go-gst/gst"
	gstnew "github.com/go-gst/go-gst/pkg/gst"
)

type Source int

const (
	videotestsrc Source = iota
	filesrc
)

type RTPStreamSourceOption func(*RTPStreamSource) error

type RTPStreamSource struct {
	source             Source
	codec              Codec
	fileSourceLocation string
	payloadType        uint

	bin      gstnew.Bin
	elements []*gst.Element
	encoder  *gst.Element
}

func RTPStreamSourcePayloadType(pt int) RTPStreamSinkOption {
	return func(rs *RTPStreamSink) error {
		rs.payloadType = pt
		return nil
	}
}

func RTPStreamSourceType(source Source) RTPStreamSourceOption {
	return func(rs *RTPStreamSource) error {
		rs.source = source
		return nil
	}
}

func RTPSTreamSourceCodec(codec Codec) RTPStreamSourceOption {
	return func(rs *RTPStreamSource) error {
		rs.codec = codec
		return nil
	}
}

func RTPStreamSourceFileSourceLocation(location string) RTPStreamSourceOption {
	return func(rs *RTPStreamSource) error {
		rs.fileSourceLocation = location
		return nil
	}
}

func NewRTPStreamSource(name string, opts ...RTPStreamSourceOption) (*RTPStreamSource, error) {
	gstnew.NewBin(name)
	s := &RTPStreamSource{
		source:             videotestsrc,
		codec:              h264,
		fileSourceLocation: "",
		payloadType:        96,
		bin:                gstnew.NewBin(name).(gstnew.Bin),
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
	if s.codec == h264 {
		s.encoder, err = gst.NewElement("x264enc")
		if err != nil {
			return nil, err
		}
		pay, err = gst.NewElement("rtph264pay")
		if err != nil {
			return nil, err
		}
		if err = SetProperties(pay, map[string]any{
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

func (s *RTPStreamSource) Element() *gst.Element {
	return s.bin.Element
}

func (s *RTPStreamSource) Link(element *gst.Element) error {
	return s.bin.Link(element)
}

func (s *RTPStreamSource) LinkPad(pad *gst.Pad) error {
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
