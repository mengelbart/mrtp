package gstreamer

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/go-gst/go-gst/gst"
	"github.com/mengelbart/mrtp"
)

type Source int

const (
	Videotestsrc Source = iota
	Filesrc
)

type StreamSourceOption func(*StreamSource) error

type StreamSource struct {
	source             Source
	codec              mrtp.Codec
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

func StreamSourceCodec(codec mrtp.Codec) StreamSourceOption {
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
		source:             Videotestsrc,
		codec:              mrtp.H264,
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

	followUpElms := make([]*gst.Element, 0)

	cs, err := gst.NewElement("clocksync")
	if err != nil {
		return nil, err
	}
	followUpElms = append(followUpElms, cs)

	var pay *gst.Element
	if s.codec == mrtp.H264 {
		settings := map[string]any{"pass": 5, "speed-preset": 1, "tune": 6, "key-int-max": 10_000}
		s.encoder, err = gst.NewElementWithProperties("x264enc", settings)
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
		followUpElms = append(followUpElms, s.encoder, pay)
	} else {
		return nil, fmt.Errorf("unknown codec: %v", s.codec)
	}

	switch s.source {
	case Videotestsrc:
		vts, err := gst.NewElement("videotestsrc")
		if err != nil {
			return nil, err
		}
		s.elements = append(s.elements, vts)
		s.elements = append(s.elements, followUpElms...)
		if err := s.bin.AddMany(s.elements...); err != nil {
			return nil, err
		}
		if err := gst.ElementLinkMany(s.elements...); err != nil {
			return nil, err
		}

		srcpad := pay.GetStaticPad("src")
		ghostpad := gst.NewGhostPad("src", srcpad)
		if !s.bin.AddPad(ghostpad.Pad) {
			return nil, errors.New("failed to add ghostpad to RTPStreamSource")
		}

	case Filesrc:
		fs, err := gst.NewElement("filesrc")
		if err != nil {
			return nil, err
		}
		fs.Set("location", s.fileSourceLocation)

		decodebin, err := gst.NewElement("decodebin")
		if err != nil {
			return nil, err
		}
		s.elements = append(s.elements, fs, decodebin)
		if err := s.bin.AddMany(s.elements...); err != nil {
			return nil, err
		}
		fs.Link(decodebin)

		// Create ghost pad with no target yet
		// will be set in decodebin callback
		ghostpad := gst.NewGhostPadNoTarget("src", gst.PadDirectionSource)
		if !s.bin.AddPad(ghostpad.Pad) {
			return nil, errors.New("failed to add ghostpad to RTPStreamSource")
		}

		// decodebin callback
		decodebin.Connect("pad-added", func(self *gst.Element, decodeSrcPad *gst.Pad) {
			var isVideo bool
			caps := decodeSrcPad.GetCurrentCaps()
			for i := 0; i < caps.GetSize(); i++ {
				st := caps.GetStructureAt(i)
				if strings.HasPrefix(st.Name(), "video/") {
					isVideo = true
				}
			}

			if !isVideo {
				return
			}

			// link follow up pipeline togehter
			if err := s.bin.AddMany(followUpElms...); err != nil {
				panic(err)
			}
			if err := gst.ElementLinkMany(followUpElms...); err != nil {
				panic(err)
			}
			s.elements = append(s.elements, followUpElms...)

			// link decodebin's src pad to the follow up pipeline
			followUpSinkPad := followUpElms[0].GetStaticPad("sink")
			if decodeSrcPad.Link(followUpSinkPad) != gst.PadLinkOK {
				panic("Failed to link decodebin to encoder")
			}

			// rest is for syncing the elements
			for _, e := range s.elements {
				e.SyncStateWithParent()
			}

			// Set ghost pad target now that pipeline exists
			srcpad := pay.GetStaticPad("src")
			if !ghostpad.SetTarget(srcpad) {
				panic("Failed to set ghostpad target")
			}
		})
	default:
		return nil, fmt.Errorf("unknown source format: %v", s.source)
	}

	return s, nil
}

func (s *StreamSource) Element() *gst.Element {
	return s.bin.Element
}

func (s *StreamSource) SrcPad() (*gst.Pad, error) {
	pad := s.bin.GetStaticPad("src")
	if pad == nil {
		return nil, errors.New("src pad not found")
	}
	return pad, nil
}

// SetBitrate sets the target bit rate of the encoder
func (s *StreamSource) SetBitrate(ratebps uint) error {
	rateKbps := ratebps / 1000

	slog.Info("NEW_TARGET_MEDIA_RATE", "rate", ratebps)

	return s.encoder.Set("bitrate", rateKbps)
}

func (s *StreamSource) EncodingName() string {
	return fmt.Sprintf("%v/%v", s.codec.MediaType(), s.codec.String())
}
