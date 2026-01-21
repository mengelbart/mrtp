package ivf

import (
	"io"
	"sync"

	"github.com/pion/webrtc/v4/pkg/media/ivfreader"
)

type PushSource struct {
	reader *ivfreader.IVFReader
	header *ivfreader.IVFFileHeader

	closeCh  chan struct{}
	isClosed bool
	lock     sync.Mutex
	wg       sync.WaitGroup
}

func NewPushSource(r io.Reader) (*PushSource, error) {
	ivfReader, ivfHeader, err := ivfreader.NewWith(r)
	if err != nil {
		return nil, err
	}
	ps := &PushSource{
		reader: ivfReader,
		header: ivfHeader,
	}
	ps.wg.Go(ps.run)
	return ps, nil
}

func (s *PushSource) run() {
	for {
		select {
		case <-s.closeCh:
			return
		default:
		}
		payload, header, err := s.reader.ParseNextFrame()
		if err != nil {
		}
	}
}

func (s *PushSource) Close() error {
	s.lock.Lock()
	defer s.lock.Unlock()
	if s.isClosed {
		return nil
	}
	close(s.closeCh)
	s.isClosed = true
	return nil
}
