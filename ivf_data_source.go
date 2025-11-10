package mrtp

import (
	"io"
	"time"

	"github.com/pion/webrtc/v4/pkg/media/ivfreader"
)

type ivfFrame struct {
	header  *ivfreader.IVFFrameHeader
	payload []byte
}

type IVFDataSource struct {
	reader *ivfreader.IVFReader
	header *ivfreader.IVFFileHeader
	closer io.Closer

	next          *ivfFrame
	last          time.Time
	frameDuration time.Duration
}

func NewIVFDataSource(reader io.ReadCloser) (*IVFDataSource, error) {
	ivfReader, ivfHeader, err := ivfreader.NewWith(reader)
	if err != nil {
		return nil, err
	}
	payload, header, err := ivfReader.ParseNextFrame()
	if err != nil {
		return nil, err
	}
	s := &IVFDataSource{
		reader: ivfReader,
		header: ivfHeader,
		closer: reader,
		next: &ivfFrame{
			header:  header,
			payload: payload,
		},
		last:          time.Time{},
		frameDuration: time.Duration((float32(ivfHeader.TimebaseNumerator) / float32(ivfHeader.TimebaseDenominator)) * 1000),
	}
	return s, nil
}

func (s *IVFDataSource) Read(buf []byte) (int, error) {
	payload, header, err := s.reader.ParseNextFrame()
	if err != nil {
		return 0, err
	}
	n := copy(buf, s.next.payload)
	s.next.payload = payload
	s.next.header = header
	s.last = <-time.After(time.Until(s.last.Add(s.frameDuration)))
	return n, nil
}

func (s *IVFDataSource) TimeUntilNextFrame() time.Duration {
	if s.last.IsZero() {
		return 0
	}
	return time.Until(s.last.Add(s.frameDuration))
}

func (s *IVFDataSource) Close() error {
	return s.closer.Close()
}
