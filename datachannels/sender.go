package datachannels

import (
	"context"
	"sync"

	"github.com/mengelbart/quicdc"
)

type Sender struct {
	dc *quicdc.DataChannel

	mw *quicdc.DataChannelWriteMessage

	closeOnce sync.Once
}

func newSender(dc *quicdc.DataChannel) *Sender {
	return &Sender{
		dc: dc,
	}
}

func (s *Sender) Write(data []byte) (int, error) {
	if s.mw == nil {
		// open new message
		var err error
		s.mw, err = s.dc.SendMessage(context.TODO())
		if err != nil {
			return 0, err
		}
	}

	n, err := s.mw.Write(data)
	if err != nil {
		return n, err
	}

	return n, nil
}

// Close closes the pending message and the data channel, which tells the peer
// that no further messages follow. Repeated calls are no-ops.
func (s *Sender) Close() error {
	var err error
	s.closeOnce.Do(func() {
		if s.mw != nil {
			err = s.mw.Close()
		}
		if closeErr := s.dc.Close(); err == nil {
			err = closeErr
		}
	})
	return err
}
