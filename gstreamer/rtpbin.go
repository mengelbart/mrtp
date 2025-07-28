package gstreamer

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/go-gst/go-gst/gst"
	"github.com/go-gst/go-gst/gst/app"
)

type RTPSourceBin interface {
	Element() *gst.Element
	SrcPad() (*gst.Pad, error)
}

type RTPSinkBin interface {
	Element() *gst.Element
	SinkPad() (*gst.Pad, error)
	ClockRate() int
	EncodingName() string
	PayloadType() int
	MediaType() string
}

type RTPBin struct {
	transports map[int]*gst.Element
	streams    map[int]RTPSinkBin

	pipeline *gst.Pipeline
	rtpbin   *gst.Element

	rtcpFunnels map[int]*gst.Element

	screamTx             *gst.Element
	SetTargetRateEncoder func(ratebps uint) error
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
		transports:           map[int]*gst.Element{},
		streams:              map[int]RTPSinkBin{},
		pipeline:             pipeline,
		rtpbin:               rtpbin,
		rtcpFunnels:          map[int]*gst.Element{},
		SetTargetRateEncoder: nil,
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

func (r *RTPBin) DebugBinToDotFile(name string) {
	r.pipeline.DebugBinToDotFile(gst.DebugGraphShowAll, name)
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
			sinkPad, err := stream.SinkPad()
			if err != nil {
				slog.Error("failed to get stream sinkpad", "error", err)
				return
			}
			if ret := pad.Link(sinkPad); ret != gst.PadLinkOK {
				slog.Error("failed to link pad", "PadLinkReturn", ret)
			}
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

func (r *RTPBin) AddRTPSourceStreamGst(id int, src RTPSourceBin, enableSCReAM bool) error {
	if err := r.pipeline.Add(src.Element()); err != nil {
		return err
	}

	sendRTPSinkPad := r.rtpbin.GetRequestPad(fmt.Sprintf("send_rtp_sink_%d", id))
	if sendRTPSinkPad == nil {
		return errors.New("failed to request sendRTPSinkPad")
	}

	srcPad, err := src.SrcPad()
	if err != nil {
		return err
	}

	if enableSCReAM {
		screamtx, err := gst.NewElementWithProperties(
			"screamtx",
			map[string]any{
				"params": "-initrate 750 -minrate 150 -maxrate 3000", // kbps
			},
		)
		if err != nil {
			return err
		}
		r.screamTx = screamtx
		if err = r.pipeline.Add(screamtx); err != nil {
			return err
		}
		if ret := srcPad.Link(screamtx.GetStaticPad("sink")); ret != gst.PadLinkOK {
			return fmt.Errorf("failed to link src pad to screamtx sink pad: %v", ret)
		}
		srcPad = screamtx.GetStaticPad("src")
		if srcPad == nil {
			return errors.New("failed to get screamtx src pad")
		}

		// every 100ms: manually get the rate of scream and set the encoder accordingly
		go func() {
			statsHeader, err := r.screamTx.GetProperty("stats-header")
			if err != nil {
				panic(err)
			}
			statsHeaderStr := statsHeader.(string)
			keys := strings.Split(statsHeaderStr, ",")
			for {
				time.Sleep(100 * time.Millisecond)

				rate, err := r.getTargetBitRate()
				if err != nil {
					panic(err)
				}
				if rate == 0 {
					// scream wants new key frame
					continue
				}
				stats, err := r.screamTx.GetProperty("stats")
				if err != nil {
					panic(err)
				}
				statsStr := stats.(string)
				values := strings.Split(statsStr, ",")
				anys := make([]any, 0, 2*len(keys))
				for i, key := range keys {
					var val any
					if strings.Contains(values[i], "Log") {
						val = strings.TrimSpace(values[i])
					} else if strings.Contains(values[i], ".") {
						val, err = strconv.ParseFloat(strings.TrimSpace(values[i]), 64)
					} else {
						val, err = strconv.Atoi(strings.TrimSpace(values[i]))
					}
					if err != nil {
						panic(err)
					}
					anys = append(anys, key, val)
				}
				slog.Info("SCReAM stats", anys...)

				err = r.SetTargetRateEncoder(rate * 1000)
				if err != nil {
					panic(err)
				}
			}
		}()
	}

	if ret := srcPad.Link(sendRTPSinkPad); ret != gst.PadLinkOK {
		return fmt.Errorf("failed to link src pad to rtp sink pad: %v", ret)
	}
	return nil
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
	funnel, ok := r.rtcpFunnels[id]
	if !ok {
		var err error
		funnel, err = gst.NewElement("funnel")
		if err != nil {
			return err
		}
		r.rtcpFunnels[id] = funnel
		if err := r.pipeline.Add(funnel); err != nil {
			return err
		}
		if !funnel.SyncStateWithParent() {
			return errors.New("failed to synchronize funnel to pipeline state")
		}
	}

	if ret := sendRTCPSrcPad.Link(funnel.GetRequestPad("sink_0")); ret != gst.PadLinkOK {
		return fmt.Errorf("failed to link RTCP src pad to RTCP funnel: %v", ret)
	}

	if err := r.pipeline.Add(sink); err != nil {
		return err
	}

	if ret := funnel.GetStaticPad("src").Link(sink.GetStaticPad("sink")); ret != gst.PadLinkOK {
		return fmt.Errorf("failed to link sendRTCPSrcPad to transport sink: %v", ret)
	}
	return nil
}

func (r *RTPBin) AddRTPSink(id int, sink RTPSinkBin) error {
	if _, ok := r.streams[id]; ok {
		return errors.New("duplicate stream id")
	}
	r.streams[id] = sink
	return nil
}

func (r *RTPBin) ReceiveRTPStreamFrom(id int, rc io.ReadCloser, screamCCFB bool) error {
	e, err := getAppSrcWithReadCloser(rc)
	if err != nil {
		return err
	}
	return r.ReceiveRTPStreamFromGst(id, e, screamCCFB)
}

func (r *RTPBin) ReceiveRTPStreamFromGst(id int, src *gst.Element, screamCCFB bool) error {
	sink, ok := r.streams[id]
	if !ok {
		return errors.New("unknown stream, did you forget to call AddRTPSink first?")
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

	var screamrx *gst.Element
	var funnel *gst.Element
	if screamCCFB {
		screamrx, err = gst.NewElement("screamrx")
		if err != nil {
			return err
		}
		if err = r.pipeline.Add(screamrx); err != nil {
			return err
		}
		srcPad := src.GetStaticPad("src")
		if ret := srcPad.Link(screamrx.GetStaticPad("sink")); ret != gst.PadLinkOK {
			panic(fmt.Sprintf("failed to link pads: %v", ret))
		}

		srcPad = screamrx.GetStaticPad("src")
		if ret := srcPad.Link(capsfilter.GetStaticPad("sink")); ret != gst.PadLinkOK {
			panic(fmt.Sprintf("failed to link pads: %v", ret))
		}

		funnel, ok = r.rtcpFunnels[id]
		if !ok {
			funnel, err = gst.NewElement("funnel")
			if err != nil {
				return err
			}
			r.rtcpFunnels[id] = funnel
			if err = r.pipeline.Add(funnel); err != nil {
				return err
			}
		}
		screamRxRTCPSrcPad := screamrx.GetStaticPad("rtcp_src")
		if ret := screamRxRTCPSrcPad.Link(funnel.GetRequestPad("sink_1")); ret != gst.PadLinkOK {
			return fmt.Errorf("faield to link screamrx RTCP src pad to funnel: %v", ret)
		}
	} else {
		if err = src.Link(capsfilter); err != nil {
			return err
		}
	}

	recvRTPSinkPad := r.rtpbin.GetRequestPad(fmt.Sprintf("recv_rtp_sink_%v", id))
	sourcePad := capsfilter.GetStaticPad("src")
	if ret := sourcePad.Link(recvRTPSinkPad); ret != gst.PadLinkOK {
		return fmt.Errorf("failed to link transport source to recvRTPSinkPad: %v", err)
	}
	if !src.SyncStateWithParent() {
		return errors.New("failed to synchronize src to pipeline state")
	}
	if !capsfilter.SyncStateWithParent() {
		return errors.New("failed to synchronize capsfilter to pipeline state")
	}
	if screamrx != nil {
		if !screamrx.SyncStateWithParent() {
			return errors.New("failed to synchronize screamrx to pipeline state")
		}
		if !funnel.SyncStateWithParent() {
			return errors.New("failed to synchronize funnel to pipeline state")
		}
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
	if r.screamTx != nil {
		if err := src.LinkFiltered(r.screamTx, gst.NewCapsFromString("application/x-rtcp")); err != nil {
			return err
		}
		src = r.screamTx
	}
	if err := src.LinkFiltered(r.rtpbin, gst.NewCapsFromString("application/x-rtcp")); err != nil {
		return err
	}
	if !src.SyncStateWithParent() {
		return errors.New("failed to sync src to pipeline state")
	}
	return nil
}

// getTargetBitRate returns the current target rate of SCReAM
func (r *RTPBin) getTargetBitRate() (uint, error) {
	if r.screamTx == nil {
		return 0, errors.New("screamTx element not initialized")
	}
	val, err := r.screamTx.GetProperty("current-max-bitrate")
	if err != nil {
		return 0, err
	}
	rate, ok := val.(uint)
	if !ok {
		return 0, fmt.Errorf("screams's current-max-bitrate not an uint")
	}

	return rate, nil
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
