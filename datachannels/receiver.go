package datachannels

import (
	"context"
	"sync"

	"github.com/mengelbart/quicdc"
)

type Receiver struct {
	dc *quicdc.DataChannel

	rm *quicdc.DataChannelReadMessage

	closeOnce sync.Once
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

// Close closes the pending message and the data channel. Repeated calls are
// no-ops.
func (r *Receiver) Close() error {
	var err error
	r.closeOnce.Do(func() {
		if r.rm != nil {
			err = r.rm.Close()
		}
		if closeErr := r.dc.Close(); err == nil {
			err = closeErr
		}
	})
	return err
}
