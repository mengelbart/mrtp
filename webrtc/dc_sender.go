package webrtc

import (
	"github.com/pion/webrtc/v4"
)

type DCsender struct {
	dc *webrtc.DataChannel
}

// newDCsender blocks until the datachannel is open
func newDCsender(dc *webrtc.DataChannel) *DCsender {
	opendChan := make(chan struct{})
	dc.OnOpen(func() {
		opendChan <- struct{}{}
	})

	// wait for open
	<-opendChan

	return &DCsender{
		dc: dc,
	}
}

func (s *DCsender) Write(data []byte) (int, error) {
	if err := s.dc.Send(data); err != nil {
		return 0, err
	}

	return len(data), nil
}

func (s *DCsender) Close() error {
	return s.dc.Close()
}
