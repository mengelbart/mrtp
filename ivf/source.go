package ivf

import (
	"fmt"
	"io"

	"github.com/pion/webrtc/v4/pkg/media/ivfreader"
)

type Source struct {
	reader *ivfreader.IVFReader
	header *ivfreader.IVFFileHeader
	closer io.Closer
}

func NewSource(rc io.ReadCloser) (*Source, error) {
	ivfReader, ivfHeader, err := ivfreader.NewWith(rc)
	if err != nil {
		return nil, err
	}
	return &Source{
		reader: ivfReader,
		header: ivfHeader,
		closer: rc,
	}, nil
}

func (s *Source) Read(buf []byte) (int, error) {
	payload, _, err := s.reader.ParseNextFrame()
	if err != nil {
		return 0, err
	}
	n := copy(buf, payload)
	if n < len(payload) {
		return n, fmt.Errorf("buffer too short, dropped remainder: %v < %v", n, len(payload))
	}
	return n, nil
}

func (s *Source) Close() error {
	return s.closer.Close()
}
