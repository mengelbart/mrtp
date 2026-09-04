//go:build cgo

package gstreamer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-gst/go-glib/glib"
	"github.com/go-gst/go-gst/gst"
	"github.com/mengelbart/mrtp"
)

type rtpSourceBin interface {
	Element() *gst.Element
	SrcPad() (*gst.Pad, error)
	MimeType() string
}

type rtpSinkBin interface {
	Element() *gst.Element
	SinkPad() (*gst.Pad, error)
	ClockRate() int
	EncodingName() string
	PayloadType() int
	MediaType() string
}

type RTPBin struct {
	pipeline *gst.Pipeline
	mainloop *glib.MainLoop
	rtpbin   *gst.Element

	// mu guards transports, streams and rtcpFunnels, which are written by
	// AddSender/AddReceiver and read from the rtpbin's own pad-added callback,
	// both of which can run concurrently.
	mu          sync.Mutex
	transports  map[int]*gst.Element
	streams     map[int]rtpSinkBin
	rtcpFunnels map[int]*gst.Element

	screamTx             *gst.Element
	SetTargetRateEncoder func(ratebps uint) error

	ctx    context.Context
	cancel context.CancelFunc
}

// EnableSCReAM adds a screamtx element, which runs SCReAM congestion control
// inside the pipeline. It has to be called before the sending stream is added.
func (r *RTPBin) EnableSCReAM(initRateKbps, minRateKbps, maxRateKbps uint) error {
	screamtx, err := gst.NewElementWithProperties(
		"screamtx",
		map[string]any{
			"params": fmt.Sprintf("-initrate %d -minrate %d -maxrate %d", initRateKbps, minRateKbps, maxRateKbps),
		},
	)
	if err != nil {
		return err
	}
	r.screamTx = screamtx
	return nil
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
	ctx, cancel := context.WithCancel(context.Background())
	r := &RTPBin{
		transports:           map[int]*gst.Element{},
		streams:              map[int]rtpSinkBin{},
		pipeline:             pipeline,
		mainloop:             glib.NewMainLoop(glib.MainContextDefault(), false),
		rtpbin:               rtpbin,
		rtcpFunnels:          map[int]*gst.Element{},
		SetTargetRateEncoder: nil,
		ctx:                  ctx,
		cancel:               cancel,
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

func (r *RTPBin) Pipeline() *gst.Pipeline {
	return r.pipeline
}

func (r *RTPBin) Run() error {
	return runPipeline(r.pipeline, r.mainloop)
}

// stop tears the pipeline down and makes a running Run return. It is safe to
// call before Run, and more than once.
func (r *RTPBin) stop() error {
	r.cancel()
	err := r.pipeline.BlockSetState(gst.StateNull)
	r.mainloop.Quit()
	return err
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
			r.mu.Lock()
			stream, ok := r.streams[id]
			r.mu.Unlock()
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
			r.mu.Lock()
			sink := r.transports[id]
			r.mu.Unlock()
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

// rtpOutput is the RTP of stream id leaving the bin, as packets of format f.
func (r *RTPBin) rtpOutput(id int, f mrtp.RTP) (mrtp.Source[mrtp.RTPPacket], error) {
	source, err := newAppSinkSource(f, mrtp.RTPBytes)
	if err != nil {
		return nil, err
	}
	if err = r.addRTPTransportSinkElement(id, source.element); err != nil {
		return nil, err
	}
	return source, nil
}

func (r *RTPBin) addRTPTransportSinkElement(id int, sink *gst.Element) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.transports[id]; ok {
		return errors.New("duplicate stream id")
	}
	r.transports[id] = sink
	return nil
}

func (r *RTPBin) addRTPSourceStream(id int, src rtpSourceBin) error {
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

	if r.screamTx != nil {
		if err = r.pipeline.Add(r.screamTx); err != nil {
			return err
		}
		if ret := srcPad.Link(r.screamTx.GetStaticPad("sink")); ret != gst.PadLinkOK {
			return fmt.Errorf("failed to link src pad to screamtx sink pad: %v", ret)
		}
		srcPad = r.screamTx.GetStaticPad("src")
		if srcPad == nil {
			return errors.New("failed to get screamtx src pad")
		}

		// every 100ms: manually get the rate of scream and set the encoder accordingly
		go func() {
			statsHeader, err := r.screamTx.GetProperty("stats-header")
			if err != nil {
				slog.Error("failed to read scream stats-header", "error", err)
				return
			}
			statsHeaderStr := statsHeader.(string)
			keys := strings.Split(statsHeaderStr, ",")
			ticker := time.NewTicker(100 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-r.ctx.Done():
					return
				case <-ticker.C:
				}

				rate, err := r.getTargetBitRate()
				if err != nil {
					slog.Error("failed to get scream target bitrate", "error", err)
					continue
				}
				if rate == 0 {
					// scream wants new key frame
					continue
				}
				stats, err := r.screamTx.GetProperty("stats")
				if err != nil {
					slog.Error("failed to read scream stats", "error", err)
					continue
				}
				statsStr := stats.(string)
				values := strings.Split(statsStr, ",")
				if len(values) < len(keys) {
					slog.Error("scream stats field count mismatch", "expected", len(keys), "got", len(values))
					continue
				}
				anys := make([]any, 0, 2*len(keys))
				parseErr := false
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
						slog.Error("failed to parse scream stats value", "key", key, "error", err)
						parseErr = true
						break
					}
					anys = append(anys, strings.TrimSpace(key), val)
				}
				if parseErr {
					continue
				}
				slog.Info("SCReAM stats", anys...)

				if r.SetTargetRateEncoder == nil {
					continue
				}
				if err := r.SetTargetRateEncoder(rate * 1000); err != nil {
					slog.Error("failed to set target rate on encoder", "error", err)
				}
			}
		}()
	}

	if ret := srcPad.Link(sendRTPSinkPad); ret != gst.PadLinkOK {
		return fmt.Errorf("failed to link src pad to rtp sink pad: %v", ret)
	}
	return nil
}

// rtcpOutput is the RTCP of stream id leaving the bin.
func (r *RTPBin) rtcpOutput(id int) (mrtp.Source[mrtp.RTCPPacket], error) {
	source, err := newAppSinkSource(mrtp.RTCP{}, mrtp.RTCPBytes)
	if err != nil {
		return nil, err
	}
	if err = r.sendRTCPForStreamElement(id, source.element); err != nil {
		return nil, err
	}
	return source, nil
}

func (r *RTPBin) sendRTCPForStreamElement(id int, sink *gst.Element) error {
	sendRTCPSrcPad := r.rtpbin.GetRequestPad(fmt.Sprintf("send_rtcp_src_%v", id))
	if sendRTCPSrcPad == nil {
		return errors.New("failed to request RTCP src pad")
	}
	r.mu.Lock()
	funnel, ok := r.rtcpFunnels[id]
	if !ok {
		var err error
		funnel, err = gst.NewElement("funnel")
		if err != nil {
			r.mu.Unlock()
			return err
		}
		r.rtcpFunnels[id] = funnel
	}
	r.mu.Unlock()
	if !ok {
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

func (r *RTPBin) addRTPSinkStream(id int, sink rtpSinkBin) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.streams[id]; ok {
		return errors.New("duplicate stream id")
	}
	r.streams[id] = sink
	return nil
}

// rtpInput takes the RTP of stream id into the bin, in the format the stream
// was built for and no other.
func (r *RTPBin) rtpInput(id int, screamCCFB bool) (mrtp.Sink[mrtp.RTPPacket], error) {
	r.mu.Lock()
	stream, ok := r.streams[id]
	r.mu.Unlock()
	if !ok {
		return nil, errors.New("unknown stream, did you forget to call addRTPSinkStream first?")
	}
	sink, err := newAppSrcSink(rtpFormatCheck(stream), mrtp.RTPBytes)
	if err != nil {
		return nil, err
	}
	if err = r.receiveRTPStreamFromElement(id, sink.element, screamCCFB); err != nil {
		return nil, err
	}
	return sink, nil
}

// rtpFormatCheck accepts the RTP format the stream's caps were built from, and
// nothing else: the bin cannot reconfigure a stream that is already wired.
func rtpFormatCheck(stream rtpSinkBin) func(mrtp.Format) error {
	return func(f mrtp.Format) error {
		format, ok := f.(mrtp.RTP)
		if !ok {
			return fmt.Errorf("expected an RTP format, got %v", f)
		}
		if int(format.PayloadType) != stream.PayloadType() ||
			int(format.ClockRate) != stream.ClockRate() ||
			format.Codec.String() != stream.EncodingName() {
			return fmt.Errorf("format %v does not match the stream's caps %v/%v pt=%v",
				format, stream.EncodingName(), stream.ClockRate(), stream.PayloadType())
		}
		return nil
	}
}

func (r *RTPBin) receiveRTPStreamFromElement(id int, src *gst.Element, screamCCFB bool) error {
	r.mu.Lock()
	sink, ok := r.streams[id]
	r.mu.Unlock()
	if !ok {
		return errors.New("unknown stream, did you forget to call addRTPSinkStream first?")
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

		r.mu.Lock()
		funnel, ok = r.rtcpFunnels[id]
		if !ok {
			funnel, err = gst.NewElement("funnel")
			if err != nil {
				r.mu.Unlock()
				return err
			}
			r.rtcpFunnels[id] = funnel
		}
		r.mu.Unlock()
		if !ok {
			if err = r.pipeline.Add(funnel); err != nil {
				return err
			}
		}
		screamRxRTCPSrcPad := screamrx.GetStaticPad("rtcp_src")
		if ret := screamRxRTCPSrcPad.Link(funnel.GetRequestPad("sink_1")); ret != gst.PadLinkOK {
			return fmt.Errorf("failed to link screamrx RTCP src pad to funnel: %v", ret)
		}
	} else {
		if err = src.Link(capsfilter); err != nil {
			return err
		}
	}

	recvRTPSinkPad := r.rtpbin.GetRequestPad(fmt.Sprintf("recv_rtp_sink_%v", id))
	sourcePad := capsfilter.GetStaticPad("src")
	if ret := sourcePad.Link(recvRTPSinkPad); ret != gst.PadLinkOK {
		return fmt.Errorf("failed to link transport source to recvRTPSinkPad: %v", ret)
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

// rtcpInput takes RTCP into the bin. It pulls, so the bin's demand decides when
// a packet is read.
func (r *RTPBin) rtcpInput() (mrtp.Consumer[mrtp.RTCPPacket], error) {
	consumer, err := newAppSrcConsumer(r.ctx, mrtp.RTCPBytes)
	if err != nil {
		return nil, err
	}
	if err = r.receiveRTCPFromElement(consumer.element); err != nil {
		return nil, err
	}
	return consumer, nil
}

func (r *RTPBin) receiveRTCPFromElement(src *gst.Element) error {
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
