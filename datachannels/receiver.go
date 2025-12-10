package datachannels

import (
	"context"

	"github.com/mengelbart/quicdc"
)

type Receiver struct {
	dc *quicdc.DataChannel

	rm *quicdc.DataChannelReadMessage
}

func newReceiver(dc *quicdc.DataChannel) *Receiver {
	return &Receiver{
		dc: dc,
	}
}

func (r *Receiver) Read(buf []byte) (int, error) {
	// open receiver stream
	if r.rm == nil {
		var err error
		r.rm, err = r.dc.ReceiveMessage(context.Background())
		if err != nil {
			return 0, err
		}
	}

	return r.rm.Read(buf)
}

func (r *Receiver) Close() error {
	return r.rm.Close()
}
