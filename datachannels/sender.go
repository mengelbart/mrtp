package datachannels

import (
	"context"
	"fmt"
	"sync"

	"github.com/mengelbart/mrtp"
	"github.com/mengelbart/quicdc"
)

// Sender writes data chunks into one data channel message.
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

// Negotiate implements mrtp.Sink.
func (s *Sender) Negotiate(f mrtp.Format) error {
	if _, ok := f.(mrtp.Data); !ok {
		return fmt.Errorf("data channel sender cannot take format %v", f)
	}
	return nil
}

// Write implements mrtp.Sink, appending one chunk to the pending message.
func (s *Sender) Write(p mrtp.Packet[mrtp.DataChunk]) error {
	defer p.Release()

	if s.mw == nil {
		// open new message
		var err error
		s.mw, err = s.dc.SendMessage(context.TODO())
		if err != nil {
			return err
		}
	}

	_, err := s.mw.Write(p.Value().Data)
	return err
}

// EndOfStream implements mrtp.Sink. It closes the pending message and the data
// channel, which tells the peer that no further messages follow.
func (s *Sender) EndOfStream() error {
	return s.Close()
}

// Close closes the pending message and the data channel. Repeated calls are
// no-ops.
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

var _ mrtp.Sink[mrtp.DataChunk] = (*Sender)(nil)
