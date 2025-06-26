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

type Sender struct {
	rtcpSendID int
	rtcpRecvID int

	transport mrtp.Transport
	streams   map[int]*StreamSource

	pipeline *gst.Pipeline
}

func NewSender(
	transport mrtp.Transport,
	streams map[int]*StreamSource,
	opts ...SenderOption,
) (*Sender, error) {
	pipeline, err := gst.NewPipeline("mrtp-sender")
	if err != nil {
		return nil, err
	}
	s := &Sender{
		rtcpSendID: 1,
		rtcpRecvID: 0,
		transport:  transport,
		streams:    streams,
		pipeline:   pipeline,
	}
	for _, opt := range opts {
		if err = opt(s); err != nil {
			return nil, err
		}
	}

	err = s.setupRTPPipeline()
	if err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Sender) Run() error {
	return gstreamer.Run(s.pipeline)
}

func (s *Sender) setupRTPPipeline() error {
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
		if !strings.HasPrefix(pad.GetName(), "send_rtp_src_") {
			return
		}
		var id int
		if _, err = fmt.Sscanf(pad.GetName(), "send_rtp_src_%d", &id); err != nil {
			return
		}
		sink := s.transport.GetSink(id)
		if err = s.pipeline.Add(sink); err != nil {
			return
		}
		if !sink.SyncStateWithParent() {
			slog.Error("failed to sync transport sink state")
			return
		}
		ret := pad.Link(sink.GetStaticPad("sink"))
		if ret != gst.PadLinkOK {
			slog.Info("failed to link pad", "PadLinkReturn", ret)
		}
		s.pipeline.DebugBinToDotFile(gst.DebugGraphShowAll, "sender-pipeline")
	})
	if err != nil {
		return err
	}

	if err = s.pipeline.Add(rtpbin); err != nil {
		return err
	}

	// Setup RTCP sender
	sendRTCPSrcPad := rtpbin.GetRequestPad("send_rtcp_src_0")
	if sendRTCPSrcPad == nil {
		return errors.New("failed to request RTCP src pad")
	}
	sink := s.transport.GetSink(s.rtcpSendID)
	if err = s.pipeline.Add(sink); err != nil {
		return err
	}
	ret := sendRTCPSrcPad.Link(sink.GetStaticPad("sink"))
	if ret != gst.PadLinkOK {
		return errors.New("failed to link sendRTCPSrcPad to transport sink")
	}

	// Setup RTCP receiver
	rtcpSource := s.transport.GetSrc(s.rtcpRecvID)
	if err = s.pipeline.Add(rtcpSource); err != nil {
		return err
	}
	if err = rtcpSource.LinkFiltered(rtpbin, gst.NewCapsFromString("application/x-rtcp")); err != nil {
		return err
	}

	// Setup media streams
	for id, stream := range s.streams {
		if err = s.pipeline.Add(stream.Element()); err != nil {
			return err
		}
		sendRTPSinkPad := rtpbin.GetRequestPad(fmt.Sprintf("send_rtp_sink_%d", id))
		if sendRTPSinkPad == nil {
			return errors.New("failed to create sendRTPSinkPad")
		}
		if err = stream.LinkPad(sendRTPSinkPad); err != nil {
			return err
		}
	}

	return nil
}
