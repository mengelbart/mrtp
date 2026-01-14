package webrtc

import (
	"github.com/pion/webrtc/v4"
)

type DCsender struct {
	dc       *webrtc.DataChannel
	dataChan chan []byte // to buffer data until BufferedAmountLow is called by pion

	firstDataUnitsSent bool // have to send one batch outside of BufferedAmountLow callback -> otherwise it is never called
}

// newDCsender blocks until the datachannel is open
func newDCsender(dc *webrtc.DataChannel) *DCsender {
	s := &DCsender{
		dc:       dc,
		dataChan: nil,
	}

	// chan size and threshold determine burtsyness
	s.dataChan = make(chan []byte, 50)
	dc.SetBufferedAmountLowThreshold(20_000)

	dc.OnBufferedAmountLow(func() {
		s.addPacketsToDc()
	})

	opendChan := make(chan struct{})
	dc.OnOpen(func() {
		opendChan <- struct{}{}
	})

	// wait for open
	<-opendChan

	return s
}

// addPacketsToDc adds all packets to the webrtc datachannel which were added unit now
func (s *DCsender) addPacketsToDc() error {
	// get length, so we only pick packets that were already in the channel
	len := len(s.dataChan)

	if len == 0 {
		s.firstDataUnitsSent = false
		return nil
	}

	for range len {
		data := <-s.dataChan
		if err := s.dc.Send(data); err != nil {
			return err
		}
	}
	return nil
}

// Write blocks if datachannel is full.
// Necessary because dc.Send does not block and creates huge buffers
func (s *DCsender) Write(data []byte) (int, error) {
	s.dataChan <- data

	if !s.firstDataUnitsSent || len(s.dataChan) == cap(s.dataChan) {
		s.firstDataUnitsSent = true
		s.addPacketsToDc() // add first batch
	}

	return len(data), nil
}

func (s *DCsender) Close() error {
	// webrtc datachannel close does not garantee all data is sent
	return nil
}
