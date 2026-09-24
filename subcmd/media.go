//go:build cgo

package subcmd

import (
	"errors"
	"flag"
	"fmt"

	"github.com/mengelbart/mrtp"
	"github.com/mengelbart/mrtp/gstreamer"
	"github.com/mengelbart/mrtp/pipeline"
)

// Names of the media pipelines selected with -media-pipeline.
const (
	mediaPipelineGst = "gst"
	mediaPipelineGo  = "go"
)

// Reserved media locations. Only sinkDiscard is a reserved word: every other
// non-empty location is a file, so a file named "discard" needs a path
// ("./discard").
const (
	// sourceTest asks for the pipeline's generated test source.
	sourceTest = ""
	// sinkDisplay asks for the media to be rendered.
	sinkDisplay = ""
	// sinkDiscard drops the media once it is decoded.
	sinkDiscard = "discard"
)

// senderConfig describes one outgoing stream.
type senderConfig struct {
	// name identifies the stream in logs and in element names.
	name        string
	codec       mrtp.Codec
	payloadType int
	// sourceLocation is the file to read media from, or sourceTest.
	sourceLocation string
	rateBounds     mrtp.RateBounds
}

// receiverConfig describes one incoming stream.
type receiverConfig struct {
	// name identifies the stream in logs and in element names.
	name        string
	codec       mrtp.Codec
	payloadType int
	// sinkLocation is the file to write media to, sinkDisplay or sinkDiscard.
	sinkLocation string
}

// rtcpEndpoints are a transport's RTCP endpoints of one stream. Either may be
// nil.
//
// recv pulls because a transport may have to keep reading whether or not the
// pipeline consumes the result, such as WebRTC pumping its interceptor chain.
type rtcpEndpoints struct {
	send mrtp.Sink[mrtp.RTCPPacket]
	recv mrtp.Puller[mrtp.RTCPPacket]
}

// sendEndpoints are a transport's endpoints of one outgoing stream. All are
// nil with -transport gst-udp, where the pipeline moves the packets itself.
type sendEndpoints struct {
	rtp mrtp.Sink[mrtp.RTPPacket]
	rtcpEndpoints
}

// receiveEndpoints are a transport's endpoints of one incoming stream, see
// sendEndpoints.
type receiveEndpoints struct {
	rtp mrtp.Source[mrtp.RTPPacket]
	rtcpEndpoints

	// rtt is the round trip time of the transport, nil if it does not know it.
	rtt mrtp.RTTSource
}

// mediaPipeline builds the media side of a command's streams.
type mediaPipeline interface {
	// addSender wires an outgoing stream into g, ending in the transport's
	// endpoints, and returns the handle rate control steers it with.
	addSender(g *pipeline.Graph, config senderConfig, t sendEndpoints) (mrtp.TargetBitrateSetter, error)
	// addReceiver wires an incoming stream into g, starting at the
	// transport's endpoints.
	addReceiver(g *pipeline.Graph, config receiverConfig, t receiveEndpoints) error
}

// mediaFlags holds the media pipeline configuration of a command.
type mediaFlags struct {
	pipeline string
	codec    string

	sourceLocation string
	sinkLocation   string

	gst    gstreamer.Config
	gopipe gopipeConfig

	commonSet bool
}

// configureCommon registers the flags that apply to both directions. It is
// idempotent, so that a command that sends and receives can call both
// configureSender and configureReceiver.
func (f *mediaFlags) configureCommon(fs *flag.FlagSet) {
	if f.commonSet {
		return
	}
	f.commonSet = true

	fs.StringVar(&f.pipeline, "media-pipeline", mediaPipelineGst,
		fmt.Sprintf("Media pipeline implementation to use (%v, %v)", mediaPipelineGst, mediaPipelineGo))
	fs.StringVar(&f.codec, "codec", mrtp.H264.String(), "Codec to encode and decode with (H264, VP8, VP9, FAKE)")
	f.gst.ConfigureFlags(fs)
	f.gopipe.configureFlags(fs)
}

// configureSender registers the flags needed to send a stream.
func (f *mediaFlags) configureSender(fs *flag.FlagSet) {
	f.configureCommon(fs)
	fs.StringVar(&f.sourceLocation, "source-location", sourceTest,
		"Media file to send. Empty, the default, uses the pipeline's generated test source.")
}

// configureReceiver registers the flags needed to receive a stream.
func (f *mediaFlags) configureReceiver(fs *flag.FlagSet) {
	f.configureCommon(fs)
	fs.StringVar(&f.sinkLocation, "sink-location", sinkDisplay,
		fmt.Sprintf("File to write the received media to, or %q to drop it. Empty, the default, renders it.", sinkDiscard))
}

// newPipeline creates the pipeline selected by -media-pipeline. Whatever
// outlives a single stream is added to runner.
func (f *mediaFlags) newPipeline(runner *pipeline.Runner) (mediaPipeline, error) {
	switch f.pipeline {
	case mediaPipelineGst:
		return newGstPipeline(&f.gst, runner)
	case mediaPipelineGo:
		return newGoPipeline(&f.gopipe)
	}
	return nil, fmt.Errorf("unknown media pipeline %q, available: %v, %v", f.pipeline, mediaPipelineGst, mediaPipelineGo)
}

// senderConfig builds the configuration of an outgoing stream from the parsed
// flags.
func (f *mediaFlags) senderConfig(name string) (senderConfig, error) {
	codec, err := mrtp.NewCodec(f.codec)
	if err != nil {
		return senderConfig{}, err
	}
	return senderConfig{
		name:           name,
		codec:          codec,
		payloadType:    mrtp.DefaultPayloadType,
		sourceLocation: f.sourceLocation,
	}, nil
}

// receiverConfig builds the configuration of an incoming stream from the
// parsed flags.
func (f *mediaFlags) receiverConfig(name string) (receiverConfig, error) {
	codec, err := mrtp.NewCodec(f.codec)
	if err != nil {
		return receiverConfig{}, err
	}
	return receiverConfig{
		name:         name,
		codec:        codec,
		payloadType:  mrtp.DefaultPayloadType,
		sinkLocation: f.sinkLocation,
	}, nil
}

// connectRTCP wires a pipeline's RTCP ports to a transport's RTCP endpoints.
// Either port may be nil, and an endpoint without a port stays unconnected
// but owned by g, so that it is still closed.
func connectRTCP(g *pipeline.Graph, out mrtp.Source[mrtp.RTCPPacket], in mrtp.Consumer[mrtp.RTCPPacket], t rtcpEndpoints) error {
	var errs []error
	switch {
	case t.send == nil && out != nil:
		errs = append(errs, errors.New("the pipeline generates RTCP, but the transport has no endpoint to send it on"))
	case t.send == nil:
	case out == nil:
		g.Add(t.send)
	default:
		errs = append(errs, g.Connect(out, t.send))
	}
	switch {
	case t.recv == nil && in != nil:
		errs = append(errs, errors.New("the pipeline expects RTCP, but the transport has no endpoint to receive it on"))
	case t.recv == nil:
	case in == nil:
		g.Add(t.recv)
	default:
		errs = append(errs, g.Attach(t.recv, in))
	}
	return errors.Join(errs...)
}
