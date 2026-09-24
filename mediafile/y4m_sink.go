package mediafile

import (
	"errors"
	"fmt"
	"image"
	"os"

	"github.com/mengelbart/mrtp"
)

// Y4MSink writes the raw frames it is given to a Y4M file.
type Y4MSink struct {
	file          *os.File
	headerWritten bool
	fpsNum        int
	fpsDen        int

	format mrtp.RawVideo
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

// Negotiate implements mrtp.Sink.
func (s *Y4MSink) Negotiate(f mrtp.Format) error {
	raw, ok := f.(mrtp.RawVideo)
	if !ok {
		return fmt.Errorf("Y4M sink takes raw video, not %v", f)
	}
	if s.headerWritten && raw != s.format {
		return errors.New("Y4M sink cannot change format mid-file")
	}
	s.format = raw
	return nil
}

// Write implements mrtp.Sink.
func (s *Y4MSink) Write(p mrtp.Packet[mrtp.RawFrame]) error {
	defer p.Release()

	if !s.headerWritten {
		chromaFormat, err := chromaFormatName(s.format.Subsampling)
		if err != nil {
			return err
		}
		// Y4M header: YUV4MPEG2 W<width> H<height> F<fps_num>:<fps_den> Ip A<aspect> C<colorspace>
		header := fmt.Sprintf("YUV4MPEG2 W%d H%d F%d:%d Ip A0:0 C%s\n",
			s.format.Width, s.format.Height, s.fpsNum, s.fpsDen, chromaFormat)
		if _, err := s.file.WriteString(header); err != nil {
			return err
		}
		s.headerWritten = true
	}

	if _, err := s.file.WriteString("FRAME\n"); err != nil {
		return err
	}
	frame := p.Value()
	for _, plane := range [][]byte{frame.Y, frame.Cb, frame.Cr} {
		if _, err := s.file.Write(plane); err != nil {
			return err
		}
	}
	return nil
}

// EndOfStream implements mrtp.Sink.
func (s *Y4MSink) EndOfStream() error {
	return nil
}

// Close implements mrtp.Element.
func (s *Y4MSink) Close() error {
	if s.file != nil {
		return s.file.Close()
	}
	return nil
}

func chromaFormatName(s image.YCbCrSubsampleRatio) (string, error) {
	switch s {
	case image.YCbCrSubsampleRatio444:
		return "444", nil
	case image.YCbCrSubsampleRatio422:
		return "422", nil
	case image.YCbCrSubsampleRatio420:
		return "420jpeg", nil
	case image.YCbCrSubsampleRatio411:
		return "411", nil
	}
	return "", fmt.Errorf("unsupported chroma subsampling format: %v", s)
}

var _ mrtp.Sink[mrtp.RawFrame] = (*Y4MSink)(nil)
