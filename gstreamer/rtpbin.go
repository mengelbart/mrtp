package gstreamer

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/go-gst/go-gst/gst"
	"github.com/go-gst/go-gst/gst/app"
)

type RTPBin struct {
	transports map[int]*gst.Element
	streams    map[int]*StreamSink

	pipeline *gst.Pipeline
	rtpbin   *gst.Element
}

func NewRTPBin(opts ...RTPBinOption) (*RTPBin, error) {
	pipeline, err := gst.NewPipeline("mrtp-rtp-bin-pipeline")
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

func (r *RTPBin) AddRTPTransportSink(id int, wc io.WriteCloser) error {
	e, err := getAppSinkWithWriteCloser(wc)
	if err != nil {
		return err
	}
	return r.AddRTPTransportSinkGst(id, e)
}

func (r *RTPBin) AddRTPTransportSinkGst(id int, sink *gst.Element) error {
	if _, ok := r.transports[id]; ok {
		return errors.New("duplicate stream id")
	}
	r.transports[id] = sink
	return nil
}

func (r *RTPBin) AddRTPStreamGst(id int, src *StreamSource) error {
	if err := r.pipeline.Add(src.Element()); err != nil {
		return err
	}
	sendRTPSinkPad := r.rtpbin.GetRequestPad(fmt.Sprintf("send_rtp_sink_%d", id))
	if sendRTPSinkPad == nil {
		return errors.New("failed to create sendRTPSinkPad")
	}
	return src.LinkPad(sendRTPSinkPad)
}

func (r *RTPBin) SendRTCPForStream(id int, wc io.WriteCloser) error {
	e, err := getAppSinkWithWriteCloser(wc)
	if err != nil {
		return err
	}
	return r.SendRTCPForStreamGst(id, e)
}

func (r *RTPBin) SendRTCPForStreamGst(id int, sink *gst.Element) error {
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

func (r *RTPBin) AddRTPReceiveStreamSinkGst(id int, sink *StreamSink) error {
	if _, ok := r.streams[id]; ok {
		return errors.New("duplicate stream id")
	}
	r.streams[id] = sink
	return nil
}

func (r *RTPBin) ReceiveRTPStreamFrom(id int, rc io.ReadCloser) error {
	e, err := getAppSrcWithReadCloser(rc)
	if err != nil {
		return err
	}
	return r.ReceiveRTPStreamFromGst(id, e)
}

func (r *RTPBin) ReceiveRTPStreamFromGst(id int, src *gst.Element) error {
	sink, ok := r.streams[id]
	if !ok {
		return errors.New("unknown stream, did you forget to call AddRTPStreamSink first?")
	}
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

func (r *RTPBin) ReceiveRTCPFrom(rc io.ReadCloser) error {
	e, err := getAppSrcWithReadCloser(rc)
	if err != nil {
		return err
	}
	return r.ReceiveRTCPFromGst(e)
}

func (r *RTPBin) ReceiveRTCPFromGst(src *gst.Element) error {
	if err := r.pipeline.Add(src); err != nil {
		return err
	}
	return src.LinkFiltered(r.rtpbin, gst.NewCapsFromString("application/x-rtcp"))
}

func getAppSinkWithWriteCloser(wc io.WriteCloser) (*gst.Element, error) {
	e, err := gst.NewElementWithProperties(
		"appsink",
		map[string]any{
			"async": false,
			"sync":  false,
		},
	)
	if err != nil {
		return nil, err
	}
	appsink := app.SinkFromElement(e)
	appsink.SetCallbacks(&app.SinkCallbacks{
		EOSFunc: func(appSink *app.Sink) {
			wc.Close()
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
			defer buffer.Unmap()

			if _, err := wc.Write(pkt); err != nil {
				return gst.FlowError
			}
			return gst.FlowOK
		},
	})
	return e, nil
}

func getAppSrcWithReadCloser(rc io.ReadCloser) (*gst.Element, error) {
	e, err := gst.NewElementWithProperties(
		"appsrc",
		map[string]any{
			"format": 3,
		},
	)
	if err != nil {
		return nil, err
	}
	src := app.SrcFromElement(e)
	src.SetStreamType(app.AppStreamTypeStream)
	src.SetCallbacks(&app.SourceCallbacks{
		NeedDataFunc: func(src *app.Source, length uint) {
			buffer := make([]byte, length)
			n, err := rc.Read(buffer)
			if err != nil {
				src.EndStream()
				return
			}
			gstBuffer := gst.NewBufferWithSize(int64(n))
			gstBuffer.Map(gst.MapWrite).WriteData(buffer[:n])
			defer gstBuffer.Unmap()
			src.PushBuffer(gstBuffer)
		},
		EnoughDataFunc: func(src *app.Source) {
			rc.Close()
		},
	})
	return e, nil
}
