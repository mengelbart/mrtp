package webrtc

import (
	"github.com/pion/webrtc/v4"
)

type RTPSender struct {
	track  *webrtc.TrackLocalStaticRTP
	sender *webrtc.RTPSender
}

func (s *RTPSender) Write(pkt []byte) (int, error) {
	return s.track.Write(pkt)
}

func (s *RTPSender) Close() error {
	return s.sender.Stop()
}
