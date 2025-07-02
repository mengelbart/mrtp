package rtp

import (
	"errors"
	"fmt"

	"github.com/go-gst/go-gst/gst"
	"github.com/mengelbart/mrtp"
)

type Sink int

const (
	autovideosink Sink = iota
	filesink
)

type StreamSinkOption func(*StreamSink) error

func StreamSinkPayloadType(pt int) StreamSinkOption {
	return func(rs *StreamSink) error {
		rs.payloadType = pt
		return nil
	}
}

type StreamSink struct {
	sink             Sink
	codec            mrtp.Codec
	fileSinkLocation string
	payloadType      int

	bin      *gst.Bin
	elements []*gst.Element
}

func NewStreamSink(name string, opts ...StreamSinkOption) (*StreamSink, error) {
	s := &StreamSink{
		sink:             autovideosink,
		codec:            mrtp.H264,
		fileSinkLocation: "",
		payloadType:      96,
		bin:              gst.NewBin(name),
		elements:         []*gst.Element{},
	}
	for _, opt := range opts {
		if err := opt(s); err != nil {
			return nil, err
		}
	}

	var err error
	var depay *gst.Element
	switch s.codec {
	case mrtp.H264:
		depay, err = gst.NewElement("rtph264depay")
		if err != nil {
			return nil, err
		}
		dec, err := gst.NewElement("avdec_h264")
		if err != nil {
			return nil, err
		}
		convert, err := gst.NewElement("videoconvert")
		if err != nil {
			return nil, err
		}
		s.elements = append(s.elements, depay, dec, convert)
	default:
		return nil, fmt.Errorf("unknown codec: %v", s.codec)
	}

	switch s.sink {
	case autovideosink:
		avs, err := gst.NewElement("autovideosink")
		if err != nil {
			return nil, err
		}
		s.elements = append(s.elements, avs)
	default:
		return nil, fmt.Errorf("unknown sink format: %v", s.sink)
	}

	if err := s.bin.AddMany(s.elements...); err != nil {
		return nil, err
	}
	if err := gst.ElementLinkMany(s.elements...); err != nil {
		return nil, err
	}

	sinkpad := depay.GetStaticPad("sink")
	ghostpad := gst.NewGhostPad("sink", sinkpad)
	if !s.bin.AddPad(ghostpad.Pad) {
		return nil, errors.New("failed to add ghostpad to RTPStreamSink")
	}

	return s, nil
}

func (s *StreamSink) Element() *gst.Element {
	return s.bin.Element
}

func (s *StreamSink) GetSinkPad() (*gst.Pad, error) {
	pads, err := s.bin.GetSinkPads()
	if err != nil {
		return nil, err
	}
	if len(pads) != 1 {
		return nil, errors.New("sink does not have exactly 1 source pad, was it initialized correctly?")
	}
	return pads[0], nil
}

func (s *StreamSink) ClockRate() int {
	return s.codec.ClockRate()
}

func (s *StreamSink) EncodingName() string {
	return s.codec.String()
}

func (s *StreamSink) PayloadType() int {
	return s.payloadType
}

func (s *StreamSink) MediaType() string {
	return s.codec.MediaType()
}
