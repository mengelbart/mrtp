package mrtp

import (
	"fmt"
	"image"
	"time"
)

// A Format says what an edge carries, with the parameters an element needs in
// order to configure itself for it. Each format pairs with one payload type by
// convention, so an element asserts the format it expects in [Sink.Negotiate].
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

// Data is the format of an edge carrying [DataChunk].
type Data struct{}

func (Data) String() string {
	return "data"
}
