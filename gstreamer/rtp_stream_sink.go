package gstreamer

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/go-gst/go-gst/gst"
	"github.com/pion/rtp"
)

type Sink int

const (
	autovideosink Sink = iota
	filesink
)

type RTPStreamSinkOption func(*RTPStreamSink) error

func RTPStreamSinkPayloadType(pt int) RTPStreamSinkOption {
	return func(rs *RTPStreamSink) error {
		rs.payloadType = pt
		return nil
	}
}

type RTPStreamSink struct {
	sink             Sink
	codec            Codec
	fileSinkLocation string
	payloadType      int

	bin      *gst.Bin
	elements []*gst.Element
}

func NewRTPStreamSink(name string, opts ...RTPStreamSinkOption) (*RTPStreamSink, error) {
	s := &RTPStreamSink{
		sink:             autovideosink,
		codec:            h264,
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
	case h264:
		depay, err = gst.NewElement("rtph264depay")
		if err != nil {
			return nil, err
		}
		depay.GetStaticPad("sink").AddProbe(gst.PadProbeTypeBuffer, func(p *gst.Pad, ppi *gst.PadProbeInfo) gst.PadProbeReturn {
			buffer := ppi.GetBuffer()
			mapinfo := buffer.Map(gst.MapRead)
			defer buffer.Unmap()
			pkt := mapinfo.AsUint8Slice()
			b := rtp.Packet{}
			if err = b.Unmarshal(pkt); err != nil {
				panic(err)
			}
			slog.Info("got rtp packet", "seqnr", b.SequenceNumber, "length", ppi.GetBuffer().GetSize())
			return gst.PadProbeOK
		})
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

func (s *RTPStreamSink) Element() *gst.Element {
	return s.bin.Element
}

func (s *RTPStreamSink) GetSinkPad() (*gst.Pad, error) {
	pads, err := s.bin.GetSinkPads()
	if err != nil {
		return nil, err
	}
	if len(pads) != 1 {
		return nil, errors.New("sink does not have exactly 1 source pad, was it initialized correctly?")
	}
	return pads[0], nil
}

func (s *RTPStreamSink) ClockRate() int {
	return s.codec.ClockRate()
}

func (s *RTPStreamSink) EncodingName() string {
	return s.codec.String()
}

func (s *RTPStreamSink) PayloadType() int {
	return s.payloadType
}

func (s *RTPStreamSink) MediaType() string {
	return s.codec.MediaType()
}
