package datachannels

import (
	"github.com/mengelbart/quicdc"
)

type Receiver struct {
	rm *quicdc.DataChannelReadMessage
}

func newReceiver(rm *quicdc.DataChannelReadMessage) *Receiver {
	return &Receiver{
		rm: rm,
	}
}

func (r *Receiver) Read(buf []byte) (int, error) {
	return r.rm.Read(buf)
}

func (r *Receiver) Close() error {
	return r.rm.Close()
}
