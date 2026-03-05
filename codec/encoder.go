package codec

import (
	"fmt"
	"image"
	"log/slog"
)

type Encoder struct {
	vpxEnc  *VPXEncoder
	x264Enc *x264encoder

	codec CodecType
}

func NewEncoder(codec CodecType) *Encoder {
	return &Encoder{
		codec: codec,
	}
}

func (e *Encoder) Link(f Writer, i Info) (Writer, error) {
	conf := Config{
		Codec:       e.codec,
		Width:       i.Width,
		Height:      i.Height,
		TargetRate:  750_000,
		TimebaseNum: i.TimebaseNum,
		TimebaseDen: i.TimebaseDen,
	}
	switch e.codec {
	case VP8, VP9:
		enc, err := NewVPXEncoder(conf)
		if err != nil {
			return nil, err
		}
		e.vpxEnc = enc
	case H264:
		enc, err := newX264encoder(conf)
		if err != nil {
			return nil, err
		}
		e.x264Enc = enc
	default:
		return nil, fmt.Errorf("unsupported codec: %v", e.codec)
	}

	frameCount := 0 // logging: plot script requires this field

	return WriterFunc(func(b []byte, a Attributes) error {
		frameDuration, err := getFrameDuration(a)
		if err != nil {
			return err
		}
		pts, err := getPTS(a)
		if err != nil {
			return err
		}

		slog.Info("encoder sink", "length", len(b), "pts", pts, "duration", frameDuration.Microseconds(), "frame-count", frameCount)

		csr, err := getChromaSubsampling(a)
		if err != nil {
			return err
		}
		image := image.NewYCbCr(
			image.Rect(0, 0, int(i.Width), int(i.Height)),
			csr,
		)

		ySize := i.Width * i.Height
		uSize := ySize / 4
		image.Y = b[:ySize]
		image.Cb = b[ySize : ySize+uSize]
		image.Cr = b[ySize+uSize:]

		var encoded *Frame
		if e.vpxEnc != nil {
			encoded, err = e.vpxEnc.Encode(image, pts, frameDuration)
			if err != nil {
				return err
			}
		} else if e.x264Enc != nil {
			encoded, err = e.x264Enc.encode(image)
			if err != nil {
				return err
			}
		}

		slog.Info("encoder src", "length", len(encoded.Payload), "pts", pts, "duration", frameDuration.Microseconds(), "keyframe", encoded.IsKeyFrame, "frame-count", frameCount)
		frameCount++

		a[IsKeyFrame] = encoded.IsKeyFrame
		return f.Write(encoded.Payload, a)
	}), nil
}

func (e *Encoder) SetTargetRate(targetRate uint64) {
	if e.vpxEnc != nil {
		e.vpxEnc.SetTargetRate(targetRate)
	} else if e.x264Enc != nil {
		e.x264Enc.setTargetRate(targetRate)
	}
}

func (e *Encoder) Close() error {
	if e.vpxEnc != nil {
		return e.vpxEnc.Close()
	} else if e.x264Enc != nil {
		return e.x264Enc.close()
	}
	return nil
}
