package webrtc

import (
	"log/slog"

	"github.com/pion/webrtc/v4"
)

const (
	bufferedAmountLowThreshold = 512 * 1024  // 512kb
	maxBufferedAmount          = 1024 * 1024 // 1mb
)

type DCsender struct {
	dc       *webrtc.DataChannel
	dataChan chan []byte // to buffer data until BufferedAmountLow is called by pion
	sendMore chan struct{}
}

// newDCsender blocks until the datachannel is open
func newDCsender(dc *webrtc.DataChannel) *DCsender {
	s := &DCsender{
		dc:       dc,
		dataChan: make(chan []byte),
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
		if err := s.sendLoop(); err != nil {
			slog.Error("Error in send loop", "error", err)
		}
	})

	<-connected

	return s
}

func (s *DCsender) sendLoop() error {
	for buf := range s.dataChan {
		if err := s.dc.Send(buf); err != nil {
			return err
		}
		if s.dc.BufferedAmount() > maxBufferedAmount {
			<-s.sendMore
		}
	}
	return nil
}

// Write blocks if datachannel is full.
// Necessary because dc.Send does not block and creates huge buffers
func (s *DCsender) Write(data []byte) (int, error) {
	s.dataChan <- data
	return len(data), nil
}

func (s *DCsender) Close() error {
	// webrtc datachannel close does not garantee all data is sent
	return nil
}
