package mrtp

import (
	"fmt"
	"image"
	"io"
	"log/slog"
	"time"

	"github.com/mengelbart/y4m"
)

type Y4MSource struct {
	FPS     float64
	reader  *y4m.Reader
	header  *y4m.StreamHeader
	encoder *Encoder

	lastFrame     time.Time
	frameDuration time.Duration
}

func NewY4MSource(reader io.Reader, encoder *Encoder) (*Y4MSource, error) {
	y4mReader, y4mHeader, err := y4m.NewReader(reader)
	if err != nil {
		return nil, err
	}
	fps := float64(y4mHeader.FrameRate.Numerator) / float64(y4mHeader.FrameRate.Denominator)
	frameDuration := time.Duration(float64(time.Second) / fps)
	return &Y4MSource{
		FPS:           fps,
		reader:        y4mReader,
		header:        y4mHeader,
		encoder:       encoder,
		lastFrame:     time.Time{},
		frameDuration: frameDuration,
	}, nil
}

func (s *Y4MSource) GetFrame() (*VP8Frame, error) {
	frame, _, err := s.reader.ReadNextFrame()
	if err != nil {
		return nil, err
	}
	image := image.NewYCbCr(image.Rect(0, 0, s.header.Width, s.header.Height), convertSubsampleRatio(s.header.ChromaSubsampling))

	ySize := s.header.Width * s.header.Height
	uSize := ySize / 4
	image.Y = frame[:ySize]
	image.Cb = frame[ySize : ySize+uSize]
	image.Cr = frame[ySize+uSize:]

	ts := s.lastFrame.Add(s.frameDuration)

	slog.Info("encoding frame", "size", len(frame))
	encoded, err := s.encoder.Encode(image, ts, s.frameDuration)
	if err != nil {
		return nil, err
	}
	return encoded, nil
}

func convertSubsampleRatio(s y4m.ChromaSubsamplingType) image.YCbCrSubsampleRatio {
	switch s {
	case y4m.CST411:
		return image.YCbCrSubsampleRatio411
	case y4m.CST420:
		return image.YCbCrSubsampleRatio420
	case y4m.CST420jpeg:
		return image.YCbCrSubsampleRatio420
	case y4m.CST420mpeg2:
		return image.YCbCrSubsampleRatio420
	case y4m.CST420paldv:
		return image.YCbCrSubsampleRatio420
	case y4m.CST422:
		return image.YCbCrSubsampleRatio422
	case y4m.CST444:
		return image.YCbCrSubsampleRatio444
	case y4m.CST444Alpha:
		return image.YCbCrSubsampleRatio444
	default:
		panic(fmt.Sprintf("unexpected y4m.ChromaSubsamplingType: %#v", s))
	}
}
