//go:build cgo

package gopipe

import (
	"errors"
	"fmt"
	"image"
	"log/slog"
	"sync"

	"github.com/mengelbart/mrtp"
	"github.com/mengelbart/mrtp/gopipe/codec"
	"github.com/mengelbart/mrtp/pipeline"
)

// Encoder encodes raw frames into coded frames.
type Encoder struct {
	vpxEnc  *codec.VPXEncoder
	x264Enc *codec.X264encoder

	codec  mrtp.Codec
	format mrtp.RawVideo
	// picture is the image handed to the codec. Its planes are swapped for the
	// current frame's on every Write, so the strides are computed once.
	picture image.YCbCr

	pool       *pipeline.Pool[mrtp.EncodedFrame]
	down       mrtp.Sink[mrtp.EncodedFrame]
	frameCount int // logging: plot script requires this field

	lock sync.Mutex
}

func NewEncoder(c mrtp.Codec) *Encoder {
	return &Encoder{
		codec: c,
		pool: pipeline.NewPool(
			func() *mrtp.EncodedFrame { return &mrtp.EncodedFrame{} },
			func(f *mrtp.EncodedFrame) { f.Data = f.Data[:0] },
		),
	}
}

// Negotiate implements mrtp.Sink. It configures the codec for the picture the
// source produces.
func (e *Encoder) Negotiate(f mrtp.Format) error {
	raw, ok := f.(mrtp.RawVideo)
	if !ok {
		return fmt.Errorf("encoder takes raw video, not %v", f)
	}
	if e.vpxEnc != nil || e.x264Enc != nil {
		return errors.New("encoder cannot be reconfigured")
	}
	e.format = raw
	e.picture = *image.NewYCbCr(
		image.Rect(0, 0, int(raw.Width), int(raw.Height)),
		raw.Subsampling,
	)

	conf := codec.Config{
		Codec:      e.codec,
		Width:      raw.Width,
		Height:     raw.Height,
		TargetRate: 750_000,
		FrameRate:  raw.FrameRate,
	}
	switch e.codec {
	case mrtp.VP8, mrtp.VP9:
		enc, err := codec.NewVPXEncoder(conf)
		if err != nil {
			return err
		}
		e.vpxEnc = enc
	case mrtp.H264:
		enc, err := codec.NewX264encoder(conf)
		if err != nil {
			return err
		}
		e.x264Enc = enc
	default:
		return fmt.Errorf("unsupported codec: %v", e.codec)
	}
	return nil
}

// Format implements mrtp.Source.
func (e *Encoder) Format() mrtp.Format {
	return mrtp.EncodedVideo{
		Codec:  e.codec,
		Width:  e.format.Width,
		Height: e.format.Height,
	}
}

// Connect implements mrtp.Source.
func (e *Encoder) Connect(down mrtp.Sink[mrtp.EncodedFrame]) error {
	if e.down != nil {
		return errors.New("gopipe: encoder is already connected")
	}
	e.down = down
	return nil
}

// Write implements mrtp.Sink.
func (e *Encoder) Write(packet mrtp.Packet[mrtp.RawFrame]) error {
	defer packet.Release()

	frame := packet.Value()
	pts := frame.PTS.Microseconds()

	slog.Debug("encoder sink", "length", len(frame.Y)+len(frame.Cb)+len(frame.Cr), "pts", pts, "duration", frame.Duration.Microseconds(), "frame-count", e.frameCount)

	e.picture.Y = frame.Y
	e.picture.Cb = frame.Cb
	e.picture.Cr = frame.Cr

	e.lock.Lock()
	var (
		encoded *codec.Frame
		err     error
	)
	if e.vpxEnc != nil {
		encoded, err = e.vpxEnc.Encode(&e.picture, pts, frame.Duration)
	} else if e.x264Enc != nil {
		encoded, err = e.x264Enc.Encode(&e.picture)
	} else {
		err = errors.New("encoder wrote before it was negotiated")
	}
	e.lock.Unlock()
	if err != nil {
		return err
	}

	slog.Debug("encoder src", "length", len(encoded.Payload), "pts", pts, "duration", frame.Duration.Microseconds(), "keyframe", encoded.IsKeyFrame, "frame-count", e.frameCount)
	e.frameCount++

	out := e.pool.Get()
	value := out.Value()
	value.Data = append(value.Data[:0], encoded.Payload...)
	value.PTS = frame.PTS
	value.Duration = frame.Duration
	value.Keyframe = encoded.IsKeyFrame

	return e.down.Write(out)
}

// EndOfStream implements mrtp.Sink.
func (e *Encoder) EndOfStream() error {
	return e.down.EndOfStream()
}

// SetTargetBitrate implements media.Sender.
func (e *Encoder) SetTargetBitrate(bitrate uint) error {
	// reduce target rate
	targetRate := uint64(0.9 * float64(bitrate))
	slog.Info("NEW_TARGET_MEDIA_RATE", "rate", targetRate)

	e.lock.Lock()
	defer e.lock.Unlock()
	if e.vpxEnc != nil {
		e.vpxEnc.SetTargetRate(targetRate)
	} else if e.x264Enc != nil {
		e.x264Enc.SetTargetRate(targetRate)
	}
	return nil
}

// Close implements mrtp.Element.
func (e *Encoder) Close() error {
	if e.vpxEnc != nil {
		return e.vpxEnc.Close()
	} else if e.x264Enc != nil {
		return e.x264Enc.Close()
	}
	return nil
}

var (
	_ mrtp.Sink[mrtp.RawFrame]       = (*Encoder)(nil)
	_ mrtp.Source[mrtp.EncodedFrame] = (*Encoder)(nil)
)
