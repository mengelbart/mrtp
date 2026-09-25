//go:build cgo

package main

import (
	"errors"

	"github.com/mengelbart/mrtp"
	"github.com/mengelbart/mrtp/internal/gstreamer"
	"github.com/mengelbart/mrtp/pipeline"
)

// errPipelineMovesRTP is what giving a transport to a pipeline started with
// -gst-udp looks like.
var errPipelineMovesRTP = errors.New("the media pipeline moves the RTP packets itself (-gst-udp), it cannot use the command's transport")

// gstPipeline builds streams on one GStreamer RTPBin.
type gstPipeline struct {
	factory *gstreamer.Factory
}

func newGstPipeline(config *gstreamer.Config, runner *pipeline.Runner) (*gstPipeline, error) {
	factory, err := config.NewFactory()
	if err != nil {
		return nil, err
	}
	// The bin's main loop carries every stream, so the run ends when it stops.
	shared := pipeline.NewGraph()
	shared.Terminal(factory.Driver())
	runner.Add(shared)
	return &gstPipeline{factory: factory}, nil
}

func (p *gstPipeline) addSender(g *pipeline.Graph, config senderConfig, t sendEndpoints) (mrtp.TargetBitrateSetter, error) {
	source, location := gstreamer.Videotestsrc, ""
	if config.sourceLocation != sourceTest {
		source, location = gstreamer.Filesrc, config.sourceLocation
	}
	stream, err := p.factory.NewSender(gstreamer.SenderConfig{
		Name:        config.name,
		Codec:       config.codec,
		PayloadType: config.payloadType,
		Source:      source,
		Location:    location,
		RateBounds:  config.rateBounds,
	})
	if err != nil {
		return nil, err
	}
	var rtpErr error
	switch {
	case stream.RTP == nil && t.rtp == nil:
	case stream.RTP == nil:
		rtpErr = errPipelineMovesRTP
	case t.rtp == nil:
		rtpErr = errGstUDPWithoutPipeline
	default:
		rtpErr = g.Connect(stream.RTP, t.rtp)
	}
	return stream.Source, errors.Join(rtpErr, connectRTCP(g, stream.RTCP, stream.Feedback, t.rtcpEndpoints))
}

func (p *gstPipeline) addReceiver(g *pipeline.Graph, config receiverConfig, t receiveEndpoints) error {
	sink, location := gstreamer.Autovideosink, ""
	switch config.sinkLocation {
	case sinkDisplay:
	case sinkDiscard:
		sink = gstreamer.Fakesink
	default:
		sink, location = gstreamer.Filesink, config.sinkLocation
	}
	stream, err := p.factory.NewReceiver(gstreamer.ReceiverConfig{
		Name:        config.name,
		Codec:       config.codec,
		PayloadType: config.payloadType,
		Sink:        sink,
		Location:    location,
	})
	if err != nil {
		return err
	}
	var rtpErr error
	switch {
	case stream.RTP == nil && t.rtp == nil:
	case stream.RTP == nil:
		rtpErr = errPipelineMovesRTP
	case t.rtp == nil:
		rtpErr = errGstUDPWithoutPipeline
	default:
		rtpErr = g.Connect(t.rtp, stream.RTP)
	}
	return errors.Join(rtpErr, connectRTCP(g, stream.RTCP, stream.Feedback, t.rtcpEndpoints))
}
