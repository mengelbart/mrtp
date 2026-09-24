package mrtp

import (
	"fmt"
	"image"
	"math"
	"time"
)

// A Format says what an edge carries, with the parameters an element needs in
// order to configure itself for it. Each format pairs with one payload type by
// convention, so an element asserts the format it expects in [Sink.Negotiate].
//
// Formats are compared with ==, by the elements that merge several inputs into
// one output, so a format is a comparable value: a struct of scalars, not a
// pointer and not a struct holding a slice or a map.
type Format interface {
	fmt.Stringer
}

// FrameRate is a nominal frame rate, in frames per second. Containers and
// codec APIs that want a timebase instead take the inverse fraction.
type FrameRate struct {
	Num, Den int
}

// Duration is how long one frame lasts.
func (r FrameRate) Duration() time.Duration {
	return time.Duration(float64(time.Second) * float64(r.Den) / float64(r.Num))
}

func (r FrameRate) String() string {
	return fmt.Sprintf("%v/%v", r.Num, r.Den)
}

// RawVideo is the format of an edge carrying [RawFrame].
type RawVideo struct {
	Width, Height uint
	Subsampling   image.YCbCrSubsampleRatio
	FrameRate     FrameRate
}

func (f RawVideo) String() string {
	return fmt.Sprintf("raw video %vx%v %v %v",
		f.Width, f.Height, f.Subsampling, f.FrameRate)
}

// PlaneSizes returns the size in bytes of the luma and of one chroma plane of
// a frame.
func (f RawVideo) PlaneSizes() (luma, chroma int, err error) {
	luma = int(f.Width * f.Height)
	switch f.Subsampling {
	case image.YCbCrSubsampleRatio420:
		return luma, luma / 4, nil
	case image.YCbCrSubsampleRatio422:
		return luma, luma / 2, nil
	case image.YCbCrSubsampleRatio444:
		return luma, luma, nil
	}
	return 0, 0, fmt.Errorf("unsupported chroma subsampling: %v", f.Subsampling)
}

// EncodedVideo is the format of an edge carrying [EncodedFrame]. It carries no
// frame rate, because the wire does not: each [EncodedFrame] times itself.
type EncodedVideo struct {
	Codec         Codec
	Width, Height uint
}

func (f EncodedVideo) String() string {
	return fmt.Sprintf("%v %vx%v", f.Codec, f.Width, f.Height)
}

// RTP is the format of an edge carrying [RTPPacket].
type RTP struct {
	Codec       Codec
	PayloadType uint8
	SSRC        uint32
	ClockRate   uint32
}

func (f RTP) String() string {
	return fmt.Sprintf("RTP %v pt=%v", f.Codec, f.PayloadType)
}

// DefaultPayloadType is the RTP payload type used when a caller does not know
// a better one.
const DefaultPayloadType = 96

// NewRTPFormat is the format of the edge carrying one stream's packets.
func NewRTPFormat(c Codec, payloadType int) (RTP, error) {
	if payloadType < 0 || payloadType > math.MaxInt8 {
		return RTP{}, fmt.Errorf("invalid payload type %v: the RTP payload type field is 7 bits, so it must be in [0, %v]", payloadType, math.MaxInt8)
	}
	return RTP{
		Codec:       c,
		PayloadType: uint8(payloadType),
		ClockRate:   uint32(c.ClockRate()),
	}, nil
}

// Data is the format of an edge carrying [DataChunk].
type Data struct{}

func (Data) String() string {
	return "data"
}

// RTCP is the format of an edge carrying [RTCPPacket]. It has no parameters,
// because pipelines generate and parse RTCP internally.
type RTCP struct{}

func (RTCP) String() string {
	return "RTCP"
}
