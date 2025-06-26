package rtp

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/mengelbart/mrtp"
	"github.com/mengelbart/mrtp/gstreamer"

	"github.com/go-gst/go-gst/gst"
)

type Receiver struct {
	rtcpSendID int
	rtcpRecvID int

	transport mrtp.Transport
	streams   map[int]*StreamSink

	pipeline *gst.Pipeline
}

func NewReceiver(transport mrtp.Transport, streams map[int]*StreamSink, opts ...ReceiverOption) (*Receiver, error) {
	pipeline, err := gst.NewPipeline("mrtp-receiver")
	if err != nil {
		return nil, err
	}
	r := &Receiver{
		rtcpSendID: 0,
		rtcpRecvID: 1,
		transport:  transport,
		streams:    streams,
		pipeline:   pipeline,
	}
	for _, opt := range opts {
		if err = opt(r); err != nil {
			return nil, err
		}
	}

	err = r.setupRTPPipeline()
	if err != nil {
		return nil, err
	}

	return r, nil
}

func (r *Receiver) Run() error {
	return gstreamer.Run(r.pipeline)
}

func (r *Receiver) setupRTPPipeline() error {
	rtpbin, err := gst.NewElementWithProperties("rtpbin", map[string]any{
		"rtp-profile": 3,
	})
	if err != nil {
		return err
	}

	_, err = rtpbin.Connect("pad-added", func(self *gst.Element, pad *gst.Pad) {
		slog.Info("PAD_ADDED", "name", pad.GetName())
		if pad.Direction() != gst.PadDirectionSource {
			return
		}
		if !strings.HasPrefix(pad.GetName(), "recv_rtp_src_") {
			return
		}
		var id, ssrc, pt int
		if _, err = fmt.Sscanf(pad.GetName(), "recv_rtp_src_%d_%d_%d", &id, &ssrc, &pt); err != nil {
			return
		}
		stream, ok := r.streams[id]
		if !ok {
			slog.Error("stream not found", "id", id)
			return
		}
		if err = r.pipeline.Add(stream.Element()); err != nil {
			slog.Error("failed to add stream to pipeline", "error", err)
			return
		}
		if !stream.Element().SyncStateWithParent() {
			slog.Error("failed to sync stream state with pipeline state")
			return
		}
		var sinkPad *gst.Pad
		sinkPad, err = stream.GetSinkPad()
		if err != nil {
			slog.Error("failed to get stream sinkpad", "error", err)
			return
		}
		if ret := pad.Link(sinkPad); ret != gst.PadLinkOK {
			slog.Error("failed to link pad", "PadLinkReturn", ret)
		}
		r.pipeline.DebugBinToDotFile(gst.DebugGraphShowAll, "receiver-pipeline")
	})
	if err != nil {
		return err
	}

	if err = r.pipeline.Add(rtpbin); err != nil {
		return err
	}

	// Setup RTCP sender
	sendRTCPSrcPad := rtpbin.GetRequestPad("send_rtcp_src_0")
	if sendRTCPSrcPad == nil {
		return errors.New("failed to request RTCP src pad")
	}
	sink := r.transport.GetSink(r.rtcpSendID)
	if err = r.pipeline.Add(sink); err != nil {
		return err
	}
	ret := sendRTCPSrcPad.Link(sink.GetStaticPad("sink"))
	if ret != gst.PadLinkOK {
		return errors.New("failed to link sendRTCPSrcPad to transport sink")
	}

	// Setup RTCP receiver
	rtcpSource := r.transport.GetSrc(r.rtcpRecvID)
	if err = r.pipeline.Add(rtcpSource); err != nil {
		return err
	}
	if err = rtcpSource.LinkFiltered(rtpbin, gst.NewCapsFromString("application/x-rtcp")); err != nil {
		return err
	}

	for id, stream := range r.streams {
		rtpSource := r.transport.GetSrc(id)
		if err = r.pipeline.Add(rtpSource); err != nil {
			return err
		}
		capsString := fmt.Sprintf(
			"application/x-rtp, clock-rate=%v,encoding-name=%v,payload=%v,media=%v",
			stream.ClockRate(),
			stream.EncodingName(),
			stream.PayloadType(),
			stream.MediaType(),
		)
		caps := gst.NewCapsFromString(capsString)

		capsfilter, err := gst.NewElement("capsfilter")
		if err != nil {
			return err
		}
		if err = capsfilter.SetProperty("caps", caps); err != nil {
			return err
		}
		if err = r.pipeline.Add(capsfilter); err != nil {
			return err
		}
		if err = rtpSource.Link(capsfilter); err != nil {
			return err
		}

		recvRTPSinkPad := rtpbin.GetRequestPad(fmt.Sprintf("recv_rtp_sink_%v", id))
		sourcePad := capsfilter.GetStaticPad("src")
		if ret := sourcePad.Link(recvRTPSinkPad); ret != gst.PadLinkOK {
			return fmt.Errorf("failed to link transport source to recvRTPSinkPad: %v", err)
		}
	}

	return nil
}
