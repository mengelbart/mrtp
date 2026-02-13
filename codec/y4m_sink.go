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

func (s *Y4MSink) SaveFrame(frame *image.YCbCr) error {
	if !s.headerWritten {
		bounds := frame.Bounds()
		width := bounds.Dx()
		height := bounds.Dy()

		// determine chroma subsampling format
		var chromaFormat string
		switch frame.SubsampleRatio {
		case image.YCbCrSubsampleRatio444:
			chromaFormat = "444"
		case image.YCbCrSubsampleRatio422:
			chromaFormat = "422"
		case image.YCbCrSubsampleRatio420:
			chromaFormat = "420jpeg"
		case image.YCbCrSubsampleRatio411:
			chromaFormat = "411"
		default:
			panic(fmt.Sprintf("unsupported chroma subsampling format: %v", frame.SubsampleRatio))
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

	// Y plane
	if _, err := s.file.Write(frame.Y); err != nil {
		return err
	}

	// Cb plane
	if _, err := s.file.Write(frame.Cb); err != nil {
		return err
	}

	// Cr plane
	if _, err := s.file.Write(frame.Cr); err != nil {
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
