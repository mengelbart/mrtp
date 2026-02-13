package codec

import (
	"fmt"
	"image"
	"io"

	"github.com/mengelbart/y4m"
)

type Y4MSource struct {
	reader *y4m.Reader
	header *y4m.StreamHeader
}

func NewY4MSource(reader io.Reader) (*Y4MSource, error) {
	y4mReader, y4mHeader, err := y4m.NewReader(reader)
	if err != nil {
		return nil, err
	}
	return &Y4MSource{
		reader: y4mReader,
		header: y4mHeader,
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

func (s *Y4MSource) GetFrame() ([]byte, Attributes, error) {
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
