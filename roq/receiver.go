package roq

import (
	"context"

	"github.com/go-gst/go-gst/gst"
	"github.com/go-gst/go-gst/gst/app"
	"github.com/mengelbart/roq"
	"github.com/pion/rtp"
)

type receiver struct {
	flow    *roq.ReceiveFlow
	element *gst.Element
}

func newReceiver(flow *roq.ReceiveFlow) (*receiver, error) {
	appsrc, err := gst.NewElementWithProperties("appsrc", map[string]any{
		"format": 3,
	})
	if err != nil {
		return nil, err
	}
	src := app.SrcFromElement(appsrc)
	src.SetStreamType(app.AppStreamTypeStream)
	pkt := make([]byte, 65535)
	src.SetCallbacks(&app.SourceCallbacks{
		NeedDataFunc: func(src *app.Source, length uint) {
			n, err := flow.Read(pkt)
			if err != nil && err != context.DeadlineExceeded {
				src.EndStream()
				return
			}
			if flow.ID() == 0 {
				b := rtp.Packet{}
				if err := b.Unmarshal(pkt[:n]); err != nil {
					panic(err)
				}
			}

			buffer := gst.NewBufferWithSize(int64(n))
			buffer.Map(gst.MapWrite).WriteData(pkt[:n])
			buffer.Unmap()
			src.PushBuffer(buffer)
		},
		EnoughDataFunc: func(src *app.Source) {
			flow.Close()
		},
	})
	return &receiver{
		flow:    flow,
		element: appsrc,
	}, nil
}
