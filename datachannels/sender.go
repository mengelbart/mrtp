package datachannels

import (
	"context"

	"github.com/mengelbart/quicdc"
)

type Sender struct {
	dc           *quicdc.DataChannel
	msgWriteCnt  int
	maxMsgWrites int

	mw *quicdc.DataChannelWriteMessage
}

func newSender(dc *quicdc.DataChannel) *Sender {
	return &Sender{
		dc:           dc,
		maxMsgWrites: 100,
	}
}

func (s *Sender) Write(data []byte) (int, error) {
	if s.mw == nil || s.msgWriteCnt >= s.maxMsgWrites {
		if s.mw != nil {
			s.mw.Close()
		}

		// open new message
		var err error
		s.mw, err = s.dc.SendMessage(context.TODO())
		if err != nil {
			return 0, err
		}
		s.msgWriteCnt = 0
	}

	n, err := s.mw.Write(data)
	if err != nil {
		return n, err
	}
	s.msgWriteCnt++

	return n, nil
}

func (s *Sender) Close() error {
	// TODO: datachannel module currently does not support closing a channel
	// at least close current message
	return s.mw.Close()
}
