package gstreamer

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/go-gst/go-gst/gst"
)

type RTPBin struct {
	transports map[int]*gst.Element
	streams    map[int]*StreamSink

	pipeline *gst.Pipeline
	rtpbin   *gst.Element
}

func NewRTPBin(opts ...RTPBinOption) (*RTPBin, error) {
	pipeline, err := gst.NewPipeline("mrtp-rtpbin")
	if err != nil {
		return nil, err
	}
	rtpbin, err := gst.NewElementWithProperties("rtpbin", map[string]any{
		"rtp-profile": 3,
	})
	if err != nil {
		return nil, err
	}
	r := &RTPBin{
		transports: map[int]*gst.Element{},
		streams:    map[int]*StreamSink{},
		pipeline:   pipeline,
		rtpbin:     rtpbin,
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

func (r *RTPBin) Run() error {
	return runPipeline(r.pipeline)
}

func (r *RTPBin) setupRTPPipeline() error {
	_, err := r.rtpbin.Connect("pad-added", func(self *gst.Element, pad *gst.Pad) {
		slog.Info("PAD_ADDED", "name", pad.GetName())
		if strings.HasPrefix(pad.GetName(), "recv_rtp_src_") {
			var id, ssrc, pt int
			if _, err := fmt.Sscanf(pad.GetName(), "recv_rtp_src_%d_%d_%d", &id, &ssrc, &pt); err != nil {
				return
			}
			stream, ok := r.streams[id]
			if !ok {
				slog.Error("stream not found", "id", id)
				return
			}
			if err := r.pipeline.Add(stream.Element()); err != nil {
				slog.Error("failed to add stream to pipeline", "error", err)
				return
			}
			if !stream.Element().SyncStateWithParent() {
				slog.Error("failed to sync stream state with pipeline state")
				return
			}
			sinkPad, err := stream.GetSinkPad()
			if err != nil {
				slog.Error("failed to get stream sinkpad", "error", err)
				return
			}
			if ret := pad.Link(sinkPad); ret != gst.PadLinkOK {
				slog.Error("failed to link pad", "PadLinkReturn", ret)
			}
			r.pipeline.DebugBinToDotFile(gst.DebugGraphShowAll, "receiver-pipeline")
		}
		if strings.HasPrefix(pad.GetName(), "send_rtp_src_") {
			var id int
			if _, err := fmt.Sscanf(pad.GetName(), "send_rtp_src_%d", &id); err != nil {
				return
			}
			sink := r.transports[id]
			if err := r.pipeline.Add(sink); err != nil {
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
			r.pipeline.DebugBinToDotFile(gst.DebugGraphShowAll, "sender-pipeline")
		}
	})
	if err != nil {
		return err
	}

	if err = r.pipeline.Add(r.rtpbin); err != nil {
		return err
	}

	return nil
}

func (r *RTPBin) SendRTCPForStreamToGst(id int, sink *gst.Element) error {
	sendRTCPSrcPad := r.rtpbin.GetRequestPad(fmt.Sprintf("send_rtcp_src_%v", id))
	if sendRTCPSrcPad == nil {
		return errors.New("failed to request RTCP src pad")
	}
	if err := r.pipeline.Add(sink); err != nil {
		return err
	}
	ret := sendRTCPSrcPad.Link(sink.GetStaticPad("sink"))
	if ret != gst.PadLinkOK {
		return errors.New("failed to link sendRTCPSrcPad to transport sink")
	}
	return nil
}

func (r *RTPBin) ReceiveRTCPFromGst(src *gst.Element) error {
	if err := r.pipeline.Add(src); err != nil {
		return err
	}
	return src.LinkFiltered(r.rtpbin, gst.NewCapsFromString("application/x-rtcp"))
}

func (r *RTPBin) ReceiveRTPStreamFromGst(id int, src *gst.Element, sink *StreamSink) error {
	if _, ok := r.streams[id]; ok {
		return errors.New("duplicate stream id")
	}
	r.streams[id] = sink

	if err := r.pipeline.Add(src); err != nil {
		return err
	}
	capsString := fmt.Sprintf(
		"application/x-rtp, clock-rate=%v,encoding-name=%v,payload=%v,media=%v",
		sink.ClockRate(),
		sink.EncodingName(),
		sink.PayloadType(),
		sink.MediaType(),
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
	if err = src.Link(capsfilter); err != nil {
		return err
	}

	recvRTPSinkPad := r.rtpbin.GetRequestPad(fmt.Sprintf("recv_rtp_sink_%v", id))
	sourcePad := capsfilter.GetStaticPad("src")
	if ret := sourcePad.Link(recvRTPSinkPad); ret != gst.PadLinkOK {
		return fmt.Errorf("failed to link transport source to recvRTPSinkPad: %v", err)
	}
	return nil
}

func (r *RTPBin) SendRTPStreamToGst(id int, src *StreamSource, sink *gst.Element) error {
	if _, ok := r.transports[id]; ok {
		return errors.New("duplicate stream id")
	}
	r.transports[id] = sink

	if err := r.pipeline.Add(src.Element()); err != nil {
		return err
	}
	sendRTPSinkPad := r.rtpbin.GetRequestPad(fmt.Sprintf("send_rtp_sink_%d", id))
	if sendRTPSinkPad == nil {
		return errors.New("failed to create sendRTPSinkPad")
	}
	return src.LinkPad(sendRTPSinkPad)
}
