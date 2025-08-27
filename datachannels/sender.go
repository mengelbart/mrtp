package datachannels

import (
	"github.com/mengelbart/quicdc"
)

type Sender struct {
	mw *quicdc.DataChannelWriteMessage
}

func newSender(mw *quicdc.DataChannelWriteMessage) *Sender {
	return &Sender{
		mw: mw,
	}
}

func (s *Sender) Write(data []byte) (int, error) {
	return s.mw.Write(data)
}

func (s *Sender) Close() error {
	return s.mw.Close()
}
