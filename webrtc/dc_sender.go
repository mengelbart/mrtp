package webrtc

import (
	"context"
	"fmt"

	"github.com/mengelbart/mrtp"
	"github.com/pion/webrtc/v4"
)

const (
	bufferedAmountLowThreshold = 512 * 1024  // 512kb
	maxBufferedAmount          = 1024 * 1024 // 1mb
)

// DCsender sends data chunks on a data channel.
type DCsender struct {
	dc       *webrtc.DataChannel
	sendMore chan struct{}
}

// newDCsender blocks until the datachannel is open or ctx is done
func newDCsender(ctx context.Context, dc *webrtc.DataChannel) (*DCsender, error) {
	s := &DCsender{
		dc:       dc,
		sendMore: make(chan struct{}, 1),
	}

	dc.SetBufferedAmountLowThreshold(bufferedAmountLowThreshold)

	connected := make(chan struct{})
	dc.OnBufferedAmountLow(func() {
		select {
		case s.sendMore <- struct{}{}:
		default:
		}
	})

	dc.OnOpen(func() {
		close(connected)
	})

	select {
	case <-connected:
		return s, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Negotiate implements mrtp.Sink.
func (s *DCsender) Negotiate(f mrtp.Format) error {
	if _, ok := f.(mrtp.Data); !ok {
		return fmt.Errorf("data channel sender cannot take format %v", f)
	}
	return nil
}

// Write implements mrtp.Sink. It blocks while the data channel is full,
// because dc.Send does not block and builds huge buffers instead.
func (s *DCsender) Write(p mrtp.Packet[mrtp.DataChunk]) error {
	defer p.Release()

	if err := s.dc.Send(p.Value().Data); err != nil {
		return err
	}
	if s.dc.BufferedAmount() > maxBufferedAmount {
		<-s.sendMore
	}
	return nil
}

// EndOfStream implements mrtp.Sink. The channel stays open, because closing a
// WebRTC data channel does not guarantee that what was sent is delivered.
func (s *DCsender) EndOfStream() error {
	return nil
}

func (s *DCsender) Close() error {
	// webrtc datachannel close does not garantee all data is sent
	return nil
}

var _ mrtp.Sink[mrtp.DataChunk] = (*DCsender)(nil)
