package webrtc

import (
	"github.com/pion/interceptor"
)

type pionRTCPReceiver interface {
	Read([]byte) (int, interceptor.Attributes, error)
}

type RTCPReceiver struct {
	receiver pionRTCPReceiver
}

func (r *RTCPReceiver) Read(buffer []byte) (int, error) {
	n, _, err := r.receiver.Read(buffer)
	return n, err
}

func (r *RTCPReceiver) Close() error {
	return nil
}
