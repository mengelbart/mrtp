package webrtc

import (
	"github.com/pion/interceptor/pkg/ccfb"
	"github.com/pion/webrtc/v4"
)

type RTPSender struct {
	track  *webrtc.TrackLocalStaticRTP
	sender *webrtc.RTPSender
	onCCFB func([]ccfb.Report)
}

func (s *RTPSender) Write(pkt []byte) (int, error) {
	return s.track.Write(pkt)
}

func (s *RTPSender) Close() error {
	return s.sender.Stop()
}

func (s *RTPSender) RTCPReceiver() *RTCPReceiver {
	return &RTCPReceiver{
		receiver: s.sender,
		onCCFB:   s.onCCFB,
	}
}
