package gstreamer

import (
	"errors"
	"fmt"

	"github.com/go-gst/go-gst/gst"
	"github.com/mengelbart/mrtp"
)

type SinkType uint

const (
	Autovideosink SinkType = iota
	Filesink
	Fakesink
)

type StreamSinkOption func(*StreamSink) error

func StreamSinkPayloadType(pt int) StreamSinkOption {
	return func(s *StreamSink) error {
		s.payloadType = pt
		return nil
	}
}

func StreamSinkType(sinkType SinkType) StreamSinkOption {
	return func(s *StreamSink) error {
		s.sinkType = sinkType
		return nil
	}
}

func StreamSinkCodec(codec mrtp.Codec) StreamSinkOption {
	return func(s *StreamSink) error {
		s.codec = codec
		return nil
	}
}

func StreamSinkLocation(location string) StreamSinkOption {
	return func(s *StreamSink) error {
		s.location = location
		return nil
	}
}

func StreamSinkFlowID(flowID uint) StreamSinkOption {
	return func(rs *StreamSink) error {
		rs.flowID = flowID
		return nil
	}
}

type StreamSink struct {
	sinkType         SinkType
	codec            mrtp.Codec
	fileSinkLocation string
	payloadType      int
	location         string

	bin      *gst.Bin
	elements []*gst.Element

	flowID uint
}

func NewStreamSink(name string, opts ...StreamSinkOption) (*StreamSink, error) {
	s := &StreamSink{
		sinkType:         Autovideosink,
		codec:            mrtp.H264,
		fileSinkLocation: "",
		payloadType:      96,
		location:         "out.y4m",
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
	var dec *gst.Element
	switch s.codec {
	case mrtp.H264:
		depay, err = gst.NewElement("rtph264depay")
		if err != nil {
			return nil, err
		}
		dec, err = gst.NewElement("avdec_h264")
		if err != nil {
			return nil, err
		}
		convert, err := gst.NewElement("videoconvert")
		if err != nil {
			return nil, err
		}
		s.elements = append(s.elements, depay, dec, convert)
	case mrtp.VP8:
		depay, err = gst.NewElement("rtpvp8depay")
		if err != nil {
			return nil, err
		}
		dec, err = gst.NewElement("vp8dec")
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

	// probe to log mapping RTP timestamp -> PTS
	depaySinkPad := depay.GetStaticPad("sink")
	depaySinkPad.AddProbe(gst.PadProbeTypeBuffer, getRTPtoPTSMappingProbe("rtp to pts mapping", s.flowID))

	switch s.sinkType {
	case Autovideosink:
		avs, err := gst.NewElement("autovideosink")
		if err != nil {
			return nil, err
		}
		s.elements = append(s.elements, avs)
	case Fakesink:
		avs, err := gst.NewElement("fakesink")
		if err != nil {
			return nil, err
		}
		s.elements = append(s.elements, avs)
	case Filesink:
		fs, err := gst.NewElementWithProperties("filesink", map[string]any{
			"location": s.location,
		})
		if err != nil {
			return nil, err
		}
		enc, err := gst.NewElement("y4menc")
		if err != nil {
			return nil, err
		}
		s.elements = append(s.elements, enc, fs)
	default:
		return nil, fmt.Errorf("unknown sink format: %v", s.sinkType)
	}

	// probe to log pts after decoder
	decSrcPad := dec.GetStaticPad("src")
	decSrcPad.AddProbe(gst.PadProbeTypeBuffer, getFrameProbe("decoder src", s.flowID))

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

func (s *StreamSink) SinkPad() (*gst.Pad, error) {
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

func (s *StreamSink) PayloadTypeName() string {
	return s.codec.String()
}

func (s *StreamSink) PayloadType() int {
	return s.payloadType
}

func (s *StreamSink) MediaType() string {
	return s.codec.MediaType()
}
