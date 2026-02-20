package codec

import (
	"image"
	"log/slog"
)

type VPXEncoder struct {
	enc *Encoder

	codec CodecType
}

func NewVPXEncoder(codec CodecType) *VPXEncoder {
	return &VPXEncoder{
		codec: codec,
	}
}

func (e *VPXEncoder) Link(f Writer, i Info) (Writer, error) {
	enc, err := NewEncoder(Config{
		Codec:       e.codec,
		Width:       i.Width,
		Height:      i.Height,
		TimebaseNum: i.TimebaseNum,
		TimebaseDen: i.TimebaseDen,
		TargetRate:  750_000,
	})
	if err != nil {
		return nil, err
	}
	e.enc = enc
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

		encoded, err := enc.Encode(image, pts, frameDuration)
		if err != nil {
			return err
		}

		slog.Info("encoder src", "length", len(encoded.Payload), "pts", pts, "duration", frameDuration.Microseconds(), "keyframe", encoded.IsKeyFrame, "frame-count", frameCount)
		frameCount++

		a[IsKeyFrame] = encoded.IsKeyFrame
		return f.Write(encoded.Payload, a)
	}), nil
}

func (e *VPXEncoder) SetTargetRate(targetRate uint64) {
	if e.enc != nil {
		e.enc.SetTargetRate(targetRate)
	}
}
