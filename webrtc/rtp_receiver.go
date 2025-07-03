package webrtc

import "github.com/pion/webrtc/v4"

type RTPReceiver struct {
	track    *webrtc.TrackRemote
	receiver *webrtc.RTPReceiver
}

func (r *RTPReceiver) Read(buffer []byte) (int, error) {
	n, _, err := r.track.Read(buffer)
	return n, err
}

func (r *RTPReceiver) Close() error {
	return r.receiver.Stop()
}

func (r *RTPReceiver) PayloadType() uint8 {
	return uint8(r.track.PayloadType())
}

func (r *RTPReceiver) RTCPReceiver() *RTCPReceiver {
	return &RTCPReceiver{
		receiver: r.receiver,
	}
}
