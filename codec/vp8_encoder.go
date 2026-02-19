package codec

import (
	"errors"
	"image"
	"log/slog"
	"time"
)

type VP8Encoder struct {
	enc *Encoder

	start time.Time
}

func NewVP8Encoder() *VP8Encoder {
	return &VP8Encoder{start: time.Time{}}
}

func (e *VP8Encoder) Link(f Writer, i Info) (Writer, error) {
	enc, err := NewEncoder(Config{
		Codec:       "vp8",
		Width:       i.Width,
		Height:      i.Height,
		TimebaseNum: i.TimebaseNum,
		TimebaseDen: i.TimebaseDen,
		TargetRate:  100_000,
	})
	if err != nil {
		return nil, err
	}
	e.enc = enc
	fps := float64(i.TimebaseNum) / float64(i.TimebaseDen)
	frameDuration := time.Duration(float64(time.Second) / fps)
	frameCount := 0 // plot script requires this field

	var lastFrame time.Time
	return WriterFunc(func(b []byte, a Attributes) error {
		// TODO: pts should be managed by source
		ts := lastFrame.Add(frameDuration)
		lastFrame = ts
		var pts int64
		if e.start.IsZero() {
			e.start = ts
		} else {
			pts = ts.Sub(e.start).Microseconds()
		}

		slog.Info("encoder sink", "length", len(b), "pts", pts, "duration", frameDuration.Microseconds(), "frame-count", frameCount)

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

		encoded, err := enc.Encode(image, pts, frameDuration)
		if err != nil {
			return err
		}

		slog.Info("encoder src", "length", len(encoded.Payload), "pts", pts, "duration", frameDuration.Microseconds(), "keyframe", encoded.IsKeyFrame, "frame-count", frameCount)
		frameCount++

		a[IsKeyFrame] = encoded.IsKeyFrame
		a[PTS] = pts
		return f.Write(encoded.Payload, a)
	}), nil
}

func (e *VP8Encoder) SetTargetRate(targetRate uint64) {
	if e.enc != nil {
		e.enc.SetTargetRate(targetRate)
	}
}
