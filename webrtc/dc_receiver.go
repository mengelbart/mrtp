package webrtc

import (
	"io"
	"runtime"

	"github.com/pion/webrtc/v4"
)

type DCreceiver struct {
	dc      *webrtc.DataChannel
	msgChan chan webrtc.DataChannelMessage
}

func newReceiver(dc *webrtc.DataChannel) *DCreceiver {
	msgChan := make(chan webrtc.DataChannelMessage, 10)
	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		msgChan <- msg
	})

	return &DCreceiver{
		dc:      dc,
		msgChan: msgChan,
	}
}

func (r *DCreceiver) Read(buf []byte) (int, error) {
	msg := <-r.msgChan

	if len(msg.Data) > len(buf) {
		return 0, io.ErrShortBuffer
	}

	println("num routines", runtime.NumGoroutine())

	copy(buf, msg.Data)

	return len(msg.Data), nil

	// return len(buf), nil
}

func (r *DCreceiver) Close() error {
	return r.dc.Close()
}
