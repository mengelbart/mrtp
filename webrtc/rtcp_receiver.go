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
	onCCFB   func([]ccfb.Report) error
}

func (r *RTCPReceiver) Read(buffer []byte) (int, error) {
	n, attr, err := r.receiver.Read(buffer)
	if err != nil {
		return n, err
	}
	data := attr.Get(ccfb.CCFBAttributesKey)
	reports, ok := data.([]ccfb.Report)
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
