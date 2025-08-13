package webrtc

import (
	"github.com/pion/interceptor"
	"github.com/pion/interceptor/pkg/rtpfb"
)

type pionRTCPReceiver interface {
	Read([]byte) (int, interceptor.Attributes, error)
}

type RTCPReceiver struct {
	receiver pionRTCPReceiver
	onCCFB   func([]rtpfb.Report) error
}

func (r *RTCPReceiver) Read(buffer []byte) (int, error) {
	n, attr, err := r.receiver.Read(buffer)
	if err != nil {
		return n, err
	}
	data := attr.Get(rtpfb.CCFBAttributesKey)
	reports, ok := data.([]rtpfb.Report)
	if ok && r.onCCFB != nil {
		err = r.onCCFB(reports)
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

func (r *RTCPReceiver) Close() error {
	return nil
}
