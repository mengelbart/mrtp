package roq

import (
	"context"
	"fmt"
	"log"

	"github.com/go-gst/go-gst/gst"
	"github.com/go-gst/go-gst/gst/app"
	"github.com/mengelbart/mrtp/gstreamer"
	"github.com/mengelbart/roq"
	"github.com/pion/rtp"
)

type SendMode int

const (
	SendModeDatagram SendMode = iota
	SendModeStreamPerPacket
	SendModeSingleStream
)

type sender struct {
	flow    *roq.SendFlow
	element *gst.Element
}

func newSender(flow *roq.SendFlow, mode SendMode) (*sender, error) {
	appsink, err := gst.NewElement("appsink")
	if err != nil {
		return nil, err
	}
	if err = gstreamer.SetProperties(appsink, map[string]any{
		"async": false,
		"sync":  false,
	}); err != nil {
		return nil, err
	}
	var stream *roq.RTPSendStream
	if mode == SendModeSingleStream {
		stream, err = flow.NewSendStream(context.TODO())
		if err != nil {
			return nil, err
		}
	}

	sink := app.SinkFromElement(appsink)
	sink.SetCallbacks(&app.SinkCallbacks{
		EOSFunc: func(appSink *app.Sink) {
			flow.Close()
		},
		NewSampleFunc: func(appSink *app.Sink) gst.FlowReturn {
			sample := appSink.PullSample()
			if sample == nil {
				return gst.FlowEOS
			}
			buffer := sample.GetBuffer()
			if buffer == nil {
				return gst.FlowEOS
			}
			pkt := buffer.Map(gst.MapRead).AsUint8Slice()
			if flow.ID() == 0 {
				b := rtp.Packet{}
				if err := b.Unmarshal(pkt); err != nil {
					panic(err)
				}
				log.Printf("sending size=%v, seqnr=%v", len(pkt), b.SequenceNumber)
			}
			switch mode {
			case SendModeDatagram:
				if err = flow.WriteRTPBytes(pkt); err != nil {
					return gst.FlowError
				}

			case SendModeStreamPerPacket:
				stream, err = flow.NewSendStream(context.TODO())
				if err != nil {
					return gst.FlowEOS
				}
				if _, err = stream.WriteRTPBytes(pkt); err != nil {
					return gst.FlowError
				}
				_ = stream.Close()

			case SendModeSingleStream:
				if _, err = stream.WriteRTPBytes(pkt); err != nil {
					return gst.FlowError
				}

			default:
				panic(fmt.Sprintf("unexpected roq.SendMode: %#v", mode))
			}
			defer buffer.Unmap()
			return gst.FlowOK
		},
	})
	return &sender{
		flow:    flow,
		element: appsink,
	}, nil
}
