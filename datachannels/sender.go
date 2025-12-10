package datachannels

import (
	"context"

	"github.com/mengelbart/quicdc"
)

type Sender struct {
	dc *quicdc.DataChannel

	mw *quicdc.DataChannelWriteMessage
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

func (s *Sender) Close() error {
	return s.mw.Close()
}
