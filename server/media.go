package server

import (
	"errors"
	"math/rand/v2"
	"time"

	"github.com/mengelbart/mrtp"
	"github.com/mengelbart/mrtp/element/fake"
	"github.com/mengelbart/mrtp/element/rtp"
	"github.com/mengelbart/mrtp/pipeline"
)

// Media of a session. Received tracks keep the codec the client negotiated.
const (
	mediaCodec  = mrtp.Fake
	sendBitrate = 1_000_000
	sendFPS     = 30
	sendMTU     = 1200
	// sendQueueDepth is how many RTP packets may wait to be sent before the
	// queue starts dropping whole frames.
	sendQueueDepth = 1000
)

// depacketizerTimeout is how long a receiver waits for a missing packet before
// giving up on the frame.
const depacketizerTimeout = 150 * time.Millisecond

// addSender wires fake video to sink into g, with the packets of each frame
// spread over the frame's duration. It returns the source rate control steers.
func addSender(g *pipeline.Graph, sink mrtp.Sink[mrtp.RTPPacket]) (*fake.Source, error) {
	g.Add(sink)
	source, err := fake.New(0, sendFPS, mrtp.RateBounds{Initial: sendBitrate, Min: sendBitrate, Max: sendBitrate})
	if err != nil {
		return nil, err
	}
	format, err := mrtp.NewRTPFormat(mediaCodec, mrtp.DefaultPayloadType)
	if err != nil {
		return nil, err
	}
	packetizer := rtp.NewPacketizer(sendMTU, format.PayloadType, rand.Uint32(), format.ClockRate, format.Codec)
	queue := pipeline.NewQueue(sendQueueDepth, pipeline.PaceFrames((*mrtp.RTPPacket).Marker, source.FrameDuration()))
	pump := pipeline.NewPump[mrtp.RTPPacket]()
	return source, errors.Join(
		g.Connect(source, packetizer),
		g.Connect(packetizer, queue),
		g.Attach(queue, pump),
		g.Connect(pump, sink),
	)
}

// addReceiver wires src through a depacketizer into a sink that drops the
// frames, and returns that sink.
func addReceiver(g *pipeline.Graph, src mrtp.Source[mrtp.RTPPacket]) (*pipeline.Discard[mrtp.EncodedFrame], error) {
	depacketizer := rtp.NewDepacketizer(depacketizerTimeout, nil)
	discard := pipeline.NewDiscard[mrtp.EncodedFrame]()
	return discard, errors.Join(g.Connect(src, depacketizer), g.Connect(depacketizer, discard))
}
