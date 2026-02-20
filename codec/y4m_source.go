package codec

import (
	"context"
	"fmt"
	"image"
	"io"
	"time"

	"github.com/mengelbart/y4m"
)

type Y4MSource struct {
	reader *y4m.Reader
	header *y4m.StreamHeader

	start time.Time
}

func NewY4MSource(reader io.Reader) (*Y4MSource, error) {
	y4mReader, y4mHeader, err := y4m.NewReader(reader)
	if err != nil {
		return nil, err
	}
	return &Y4MSource{
		reader: y4mReader,
		header: y4mHeader,
		start:  time.Time{},
	}, nil
}

func (s *Y4MSource) GetInfo() Info {
	return Info{
		Width:       uint(s.header.Width),
		Height:      uint(s.header.Height),
		TimebaseNum: s.header.FrameRate.Numerator,
		TimebaseDen: s.header.FrameRate.Denominator,
	}
}

func (s *Y4MSource) getFrame() ([]byte, Attributes, error) {
	frame, _, err := s.reader.ReadNextFrame()
	if err != nil {
		return nil, nil, err
	}
	csr := convertSubsampleRatio(s.header.ChromaSubsampling)

	attr := Attributes{
		ChromaSubsampling: csr,
	}

	return frame, attr, nil
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

// StartLive starts the source as live source.
func (s *Y4MSource) StartLive(ctx context.Context, pipeline Writer) error {
	fps := float64(s.header.FrameRate.Numerator) / float64(s.header.FrameRate.Denominator)
	frameDuration := time.Duration(float64(time.Second) / fps)

	var lastFrame time.Time

	ticker := time.NewTicker(frameDuration)
	defer ticker.Stop()
	for range ticker.C {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		frame, attr, err := s.getFrame()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}

		// create pts
		ts := lastFrame.Add(frameDuration)
		lastFrame = ts
		var pts int64
		if s.start.IsZero() {
			s.start = ts
		} else {
			pts = ts.Sub(s.start).Microseconds()
		}

		attr[PTS] = pts
		attr[FrameDuration] = frameDuration
		err = pipeline.Write(frame, attr)
		if err != nil {
			return err
		}
	}

	return nil
}
