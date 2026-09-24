package webrtc

import (
	"errors"
	"fmt"
	"sync"

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
	opened   chan struct{}

	closeOnce sync.Once
	closed    chan struct{}
}

func newDCsender(dc *webrtc.DataChannel) *DCsender {
	s := &DCsender{
		dc:       dc,
		sendMore: make(chan struct{}, 1),
		opened:   make(chan struct{}),
		closed:   make(chan struct{}),
	}

	dc.SetBufferedAmountLowThreshold(bufferedAmountLowThreshold)

	dc.OnBufferedAmountLow(func() {
		select {
		case s.sendMore <- struct{}{}:
		default:
		}
	})

	dc.OnOpen(func() {
		close(s.opened)
	})
	return s
}

// Negotiate implements mrtp.Sink.
func (s *DCsender) Negotiate(f mrtp.Format) error {
	if _, ok := f.(mrtp.Data); !ok {
		return fmt.Errorf("data channel sender cannot take format %v", f)
	}
	return nil
}

// Write implements mrtp.Sink. It blocks until the data channel is open, and
// while it is full, because dc.Send does not block and builds huge buffers
// instead.
func (s *DCsender) Write(p mrtp.Packet[mrtp.DataChunk]) error {
	defer p.Release()

	select {
	case <-s.opened:
	case <-s.closed:
		return errors.New("webrtc: data channel sender is closed")
	}

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

// Close implements mrtp.Element. It leaves the data channel open, because
// closing it does not guarantee that what was sent is delivered.
func (s *DCsender) Close() error {
	s.closeOnce.Do(func() { close(s.closed) })
	return nil
}

var _ mrtp.Sink[mrtp.DataChunk] = (*DCsender)(nil)
