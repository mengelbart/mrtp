package codec

import (
	"fmt"
	"image"
	"os"
)

type Y4MSink struct {
	file          *os.File
	headerWritten bool
	fpsNum        int
	fpsDen        int
}

func NewY4MSink(filePath string, fpsNum, fpsDen int) (*Y4MSink, error) {
	file, err := os.Create(filePath)
	if err != nil {
		return nil, err
	}

	return &Y4MSink{
		file:   file,
		fpsNum: fpsNum,
		fpsDen: fpsDen,
	}, nil
}

func (s *Y4MSink) SaveFrame(frameData []byte, width, height int, subsampling image.YCbCrSubsampleRatio) error {
	if !s.headerWritten {
		// determine chroma subsampling format
		var chromaFormat string
		switch subsampling {
		case image.YCbCrSubsampleRatio444:
			chromaFormat = "444"
		case image.YCbCrSubsampleRatio422:
			chromaFormat = "422"
		case image.YCbCrSubsampleRatio420:
			chromaFormat = "420jpeg"
		case image.YCbCrSubsampleRatio411:
			chromaFormat = "411"
		default:
			return fmt.Errorf("unsupported chroma subsampling format: %v", subsampling)
		}

		// Y4M header: YUV4MPEG2 W<width> H<height> F<fps_num>:<fps_den> Ip A<aspect> C<colorspace>
		header := fmt.Sprintf("YUV4MPEG2 W%d H%d F%d:%d Ip A0:0 C%s\n", width, height, s.fpsNum, s.fpsDen, chromaFormat)
		if _, err := s.file.WriteString(header); err != nil {
			return err
		}
		s.headerWritten = true
	}

	// frame header
	if _, err := s.file.WriteString("FRAME\n"); err != nil {
		return err
	}

	// write YUV data directly
	if _, err := s.file.Write(frameData); err != nil {
		return err
	}

	return nil
}

func (s *Y4MSink) Close() error {
	if s.file != nil {
		return s.file.Close()
	}
	return nil
}

// Write implements the Writer interface for Y4MSink.
// For use in the processing pipeline.
func (a *Y4MSink) Write(b []byte, attrs Attributes) error {
	// parse attributes
	width, err := getWidth(attrs)
	if err != nil {
		return fmt.Errorf("Y4MSink: %w", err)
	}

	height, err := getHeight(attrs)
	if err != nil {
		return fmt.Errorf("Y4MSink: %w", err)
	}

	subsampleRatio, err := getChromaSubsampling(attrs)
	if err != nil {
		return fmt.Errorf("Y4MSink: %w", err)
	}

	return a.SaveFrame(b, width, height, subsampleRatio)
}
