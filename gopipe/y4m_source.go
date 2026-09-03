package gopipe

import (
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"time"

	"github.com/mengelbart/mrtp"
	"github.com/mengelbart/mrtp/pipeline"
	"github.com/mengelbart/y4m"
)

// Y4MSource reads a Y4M stream and pushes one raw frame per frame interval.
type Y4MSource struct {
	reader *y4m.Reader
	header *y4m.StreamHeader

	format mrtp.RawVideo
	pool   *pipeline.Pool[mrtp.RawFrame]
	down   mrtp.Sink[mrtp.RawFrame]
}

func NewY4MSource(reader io.Reader) (*Y4MSource, error) {
	y4mReader, y4mHeader, err := y4m.NewReader(reader)
	if err != nil {
		return nil, err
	}
	format := mrtp.RawVideo{
		Width:       uint(y4mHeader.Width),
		Height:      uint(y4mHeader.Height),
		Subsampling: convertSubsampleRatio(y4mHeader.ChromaSubsampling),
		FrameRate: mrtp.FrameRate{
			Num: y4mHeader.FrameRate.Numerator,
			Den: y4mHeader.FrameRate.Denominator,
		},
	}
	ySize, cSize, err := planeSizes(format)
	if err != nil {
		return nil, err
	}
	return &Y4MSource{
		reader: y4mReader,
		header: y4mHeader,
		format: format,
		pool: pipeline.NewPool(func() *mrtp.RawFrame {
			buffer := make([]byte, ySize+2*cSize)
			return &mrtp.RawFrame{
				Y:  buffer[:ySize],
				Cb: buffer[ySize : ySize+cSize],
				Cr: buffer[ySize+cSize:],
			}
		}, nil),
	}, nil
}

// planeSizes is the size in bytes of the luma and of one chroma plane of a
// frame in format f.
func planeSizes(f mrtp.RawVideo) (luma, chroma int, err error) {
	luma = int(f.Width * f.Height)
	switch f.Subsampling {
	case image.YCbCrSubsampleRatio420:
		return luma, luma / 4, nil
	case image.YCbCrSubsampleRatio422:
		return luma, luma / 2, nil
	case image.YCbCrSubsampleRatio444:
		return luma, luma, nil
	}
	return 0, 0, fmt.Errorf("unsupported chroma subsampling: %v", f.Subsampling)
}

// Format implements mrtp.Source.
func (s *Y4MSource) Format() mrtp.Format {
	return s.format
}

// Connect implements mrtp.Source.
func (s *Y4MSource) Connect(down mrtp.Sink[mrtp.RawFrame]) error {
	if s.down != nil {
		return errors.New("gopipe: Y4M source is already connected")
	}
	s.down = down
	return nil
}

// Run implements mrtp.Driver. It reads one frame per frame interval, so the
// source runs at the rate a live capture would.
func (s *Y4MSource) Run(ctx context.Context) error {
	if s.down == nil {
		return errors.New("gopipe: Y4M source runs with its output wired")
	}
	frameDuration := s.format.FrameRate.Duration()

	var pts time.Duration

	ticker := time.NewTicker(frameDuration)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}

		frame, _, err := s.reader.ReadNextFrame()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return s.down.EndOfStream()
			}
			return err
		}

		packet := s.pool.Get()
		value := packet.Value()
		if len(frame) < len(value.Y)+len(value.Cb)+len(value.Cr) {
			packet.Release()
			return fmt.Errorf("gopipe: short Y4M frame: got %v bytes, want %v",
				len(frame), len(value.Y)+len(value.Cb)+len(value.Cr))
		}
		n := copy(value.Y, frame)
		n += copy(value.Cb, frame[n:])
		copy(value.Cr, frame[n:])
		value.PTS = pts
		value.Duration = frameDuration

		pts += frameDuration

		if err := s.down.Write(packet); err != nil {
			return err
		}
	}
}

// Close implements mrtp.Element. The reader is owned by whoever opened it.
func (s *Y4MSource) Close() error {
	return nil
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

var (
	_ mrtp.Source[mrtp.RawFrame] = (*Y4MSource)(nil)
	_ mrtp.Driver                = (*Y4MSource)(nil)
)
