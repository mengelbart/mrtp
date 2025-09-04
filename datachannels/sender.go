package datachannels

import (
	"context"

	"github.com/mengelbart/quicdc"
)

type Sender struct {
	dc *quicdc.DataChannel
}

func newSender(dc *quicdc.DataChannel) *Sender {
	return &Sender{
		dc: dc,
	}
}

func (s *Sender) Write(data []byte) (int, error) {
	mw, err := s.dc.SendMessage(context.TODO())
	if err != nil {
		return 0, err
	}

	n, err := mw.Write(data)
	if err != nil {
		return n, err
	}

	return n, mw.Close()
}

func (s *Sender) Close() error {
	// TODO
	return nil
}
