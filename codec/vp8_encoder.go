package codec

import (
	"errors"
	"image"
	"log/slog"
	"time"
)

type VP8Encoder struct {
}

func NewVP8Encoder() *VP8Encoder {
	return &VP8Encoder{}
}

func (e *VP8Encoder) Link(f Writer, i Info) (Writer, error) {
	enc, err := NewEncoder(Config{
		Codec:       "vp8",
		Width:       i.Width,
		Heigth:      i.Height,
		TimebaseNum: i.TimebaseNum,
		TimebaseDen: i.TimebaseDen,
		TargetRate:  100_000,
	})
	if err != nil {
		return nil, err
	}
	fps := float64(i.TimebaseNum) / float64(i.TimebaseDen)
	frameDuration := time.Duration(float64(time.Second) / fps)
	var lastFrame time.Time
	return WriterFunc(func(b []byte, a Attributes) error {
		csa, ok := a[ChromaSubsampling]
		if !ok {
			return errors.New("missing chroma subsampling type")
		}
		csr, ok := csa.(image.YCbCrSubsampleRatio)
		if !ok {
			return errors.New("invalid chroma subsampling ratio")
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

		ts := lastFrame.Add(frameDuration)
		lastFrame = ts

		start := time.Now()
		encoded, err := enc.Encode(image, ts, frameDuration)
		end := time.Now()
		if err != nil {
			return err
		}
		slog.Info("encoded frame", "raw-size", len(b), "encoded-size", len(encoded.Payload), "pts", ts, "duration", frameDuration, "keyframe", encoded.IsKeyFrame, "latency", end.Sub(start))
		a[IsKeyFrame] = encoded.IsKeyFrame
		return f.Write(encoded.Payload, a)
	}), nil
}
