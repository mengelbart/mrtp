package webrtc

import (
	"github.com/pion/webrtc/v4"
)

type RTPReceiver struct {
	track    *webrtc.TrackRemote
	receiver *webrtc.RTPReceiver
}

func (r *RTPReceiver) Read(buffer []byte) (int, error) {
	n, _, err := r.track.Read(buffer)
	if err != nil {
		return n, err
	}
	return n, err
}

func (r *RTPReceiver) Close() error {
	return r.receiver.Stop()
}

func (r *RTPReceiver) Codec() webrtc.RTPCodecParameters {
	return r.track.Codec()
}

func (r *RTPReceiver) ID() string {
	return r.track.ID()
}

func (r *RTPReceiver) SSRC() uint32 {
	return uint32(r.track.SSRC())
}

func (r *RTPReceiver) PayloadType() uint8 {
	return uint8(r.track.PayloadType())
}

func (r *RTPReceiver) RTCPReceiver() *RTCPReceiver {
	return &RTCPReceiver{
		receiver: r.receiver,
	}
}
