// Package codec defines the interfaces shared by the codec implementations in
// its sub-packages.
package codec

import (
	"errors"
	"image"
	"time"

	"github.com/mengelbart/mrtp"
)

// ErrFrameNotReady is returned by a Decoder that needs more input before it
// can emit a frame.
var ErrFrameNotReady = errors.New("codec: frame not ready, decoder needs more input packets")

// Config configures an Encoder.
type Config struct {
	Codec      mrtp.Codec
	Width      uint
	Height     uint
	FrameRate  mrtp.FrameRate
	TargetRate uint64
}

// Frame is an encoded frame.
type Frame struct {
	IsKeyFrame bool
	Payload    []byte
}

// DecodedFrame is a raw frame with its planes stored contiguously.
type DecodedFrame struct {
	Data              []byte
	Width             int
	Height            int
	ChromaSubsampling image.YCbCrSubsampleRatio
}

// Encoder encodes raw pictures into frames.
type Encoder interface {
	Encode(image *image.YCbCr, pts int64, duration time.Duration) (*Frame, error)
	SetTargetRate(bitrate uint64)
	Close() error
}

// Decoder decodes frames into raw pictures.
type Decoder interface {
	Decode(encFrame []byte) (*DecodedFrame, error)
	Close() error
}
