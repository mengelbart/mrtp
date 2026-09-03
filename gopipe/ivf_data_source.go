package gopipe

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/mengelbart/mrtp"
	"github.com/mengelbart/mrtp/pipeline"
	"github.com/pion/webrtc/v4/pkg/media/ivfreader"
)

type IVFDataSource struct {
	reader *ivfreader.IVFReader
	header *ivfreader.IVFFileHeader
	closer io.Closer

	next          []byte
	last          time.Time
	frameDuration time.Duration

	format mrtp.EncodedVideo
	pool   *pipeline.Pool[mrtp.EncodedFrame]
	pts    time.Duration
}

// codecFromFourCC maps an IVF FourCC to the codec of its frames.
func codecFromFourCC(fourCC string) (mrtp.Codec, error) {
	switch fourCC {
	case "VP80":
		return mrtp.VP8, nil
	case "VP90":
		return mrtp.VP9, nil
	}
	return 0, fmt.Errorf("unsupported IVF FourCC: %q", fourCC)
}

func NewIVFDataSource(reader io.ReadCloser) (*IVFDataSource, error) {
	ivfReader, ivfHeader, err := ivfreader.NewWith(reader)
	if err != nil {
		return nil, err
	}
	codec, err := codecFromFourCC(ivfHeader.FourCC)
	if err != nil {
		return nil, err
	}
	payload, _, err := ivfReader.ParseNextFrame()
	if err != nil {
		return nil, err
	}
	// IVF stores its timebase as scale/rate, the inverse of a frame rate
	rate := mrtp.FrameRate{
		Num: int(ivfHeader.TimebaseDenominator),
		Den: int(ivfHeader.TimebaseNumerator),
	}
	return &IVFDataSource{
		reader:        ivfReader,
		header:        ivfHeader,
		closer:        reader,
		next:          payload,
		frameDuration: rate.Duration(),
		format: mrtp.EncodedVideo{
			Codec:  codec,
			Width:  uint(ivfHeader.Width),
			Height: uint(ivfHeader.Height),
		},
		pool: pipeline.NewPool(
			func() *mrtp.EncodedFrame { return &mrtp.EncodedFrame{} },
			func(f *mrtp.EncodedFrame) { f.Data = f.Data[:0] },
		),
	}, nil
}

// Format implements mrtp.Puller.
func (s *IVFDataSource) Format() mrtp.Format {
	return s.format
}

// Pull implements mrtp.Puller.
func (s *IVFDataSource) Pull(ctx context.Context) (mrtp.Packet[mrtp.EncodedFrame], error) {
	if s.next == nil {
		return nil, io.EOF
	}
	if !s.last.IsZero() {
		timer := time.NewTimer(time.Until(s.last.Add(s.frameDuration)))
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case now := <-timer.C:
			s.last = now
		}
	} else {
		s.last = time.Now()
	}

	packet := s.pool.Get()
	value := packet.Value()
	value.Data = append(value.Data[:0], s.next...)
	value.PTS = s.pts
	value.Duration = s.frameDuration
	s.pts += s.frameDuration

	payload, _, err := s.reader.ParseNextFrame()
	if err != nil {
		s.next = nil
	} else {
		s.next = payload
	}
	return packet, nil
}

// Close implements mrtp.Element.
func (s *IVFDataSource) Close() error {
	return s.closer.Close()
}

var _ mrtp.Puller[mrtp.EncodedFrame] = (*IVFDataSource)(nil)
