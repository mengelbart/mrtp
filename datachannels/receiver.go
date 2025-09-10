package datachannels

import (
	"context"
	"io"

	"github.com/mengelbart/quicdc"
)

type Receiver struct {
	dc *quicdc.DataChannel
}

func newReceiver(dc *quicdc.DataChannel) *Receiver {
	return &Receiver{
		dc: dc,
	}
}

func (r *Receiver) Read(buf []byte) (int, error) {
	// open receiver stream
	rm, err := r.dc.ReceiveMessage(context.Background())
	if err != nil {
		return 0, err
	}

	n, err := rm.Read(buf)
	if err != nil && err != io.EOF {
		return n, err
	}

	return n, nil
}

func (r *Receiver) Close() error {
	// TODO: datachannel module currently does not support closing a channel
	return nil
}
