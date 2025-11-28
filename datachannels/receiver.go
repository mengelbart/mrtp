package datachannels

import (
	"context"
	"io"

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

	n, err := r.rm.Read(buf)
	if err != nil {
		if err == io.EOF {
			// finished reading this message
			r.rm = nil
			return n, nil
		}

		return n, err
	}

	return n, nil
}

func (r *Receiver) Close() error {
	// TODO: datachannel module currently does not support closing a channel
	return nil
}
