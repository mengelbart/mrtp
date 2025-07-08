package webrtc

import (
	"github.com/pion/interceptor"
	"github.com/pion/interceptor/pkg/ccfb"
)

type pionRTCPReceiver interface {
	Read([]byte) (int, interceptor.Attributes, error)
}

type RTCPReceiver struct {
	receiver pionRTCPReceiver
	onCCFB   func([]ccfb.Report)
}

func (r *RTCPReceiver) Read(buffer []byte) (int, error) {
	n, attr, err := r.receiver.Read(buffer)
	data := attr.Get(ccfb.CCFBAttributesKey)
	reports, ok := data.([]ccfb.Report)
	if ok && r.onCCFB != nil {
		r.onCCFB(reports)
	}
	return n, err
}

func (r *RTCPReceiver) Close() error {
	return nil
}
