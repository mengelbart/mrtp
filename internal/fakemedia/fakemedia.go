// Package fakemedia builds the pure Go media graphs of the signaling client
// and server: a paced sender of Fake video, and a receiver that depacketizes
// and drops frames.
package fakemedia

import (
	"errors"
	"math/rand/v2"
	"time"

	"github.com/mengelbart/mrtp"
	"github.com/mengelbart/mrtp/element/fake"
	"github.com/mengelbart/mrtp/element/rtp"
	"github.com/mengelbart/mrtp/pipeline"
)

// Codec is the codec senders send.
const Codec = mrtp.Fake

const (
	// sendQueueDepth is how many RTP packets may wait to be sent before the
	// queue starts dropping whole frames.
	sendQueueDepth = 1000
	// depacketizerTimeout is how long a receiver waits for a missing packet
	// before giving up on the frame.
	depacketizerTimeout = 150 * time.Millisecond
)

// SenderConfig configures a sender.
type SenderConfig struct {
	// Duration is how long the sender sends, 0 for no limit.
	Duration time.Duration
	FPS      uint64
	// MTU is the maximum RTP packet size in bytes.
	MTU    uint16
	Bounds mrtp.RateBounds
}

// AddSender wires Fake video to sink into g, with the packets of each frame
// spread over the frame's duration. The graph ends when the sender does. It
// returns the source rate control steers.
func AddSender(g *pipeline.Graph, config SenderConfig, sink mrtp.Sink[mrtp.RTPPacket]) (*fake.Source, error) {
	g.Add(sink)
	source, err := fake.New(config.Duration, config.FPS, config.Bounds)
	if err != nil {
		return nil, err
	}
	format, err := mrtp.NewRTPFormat(Codec, mrtp.DefaultPayloadType)
	if err != nil {
		return nil, err
	}
	packetizer := rtp.NewPacketizer(config.MTU, format.PayloadType, rand.Uint32(), format.ClockRate, format.Codec)
	queue := pipeline.NewQueue(sendQueueDepth, pipeline.PaceFrames((*mrtp.RTPPacket).Marker, source.FrameDuration()))
	pump := pipeline.NewPump[mrtp.RTPPacket]()
	g.Terminal(source)
	return source, errors.Join(
		g.Connect(source, packetizer),
		g.Connect(packetizer, queue),
		g.Attach(queue, pump),
		g.Connect(pump, sink),
	)
}

// AddReceiver wires src through a depacketizer into a sink that drops the
// frames, and returns that sink.
func AddReceiver(g *pipeline.Graph, src mrtp.Source[mrtp.RTPPacket]) (*pipeline.Discard[mrtp.EncodedFrame], error) {
	depacketizer := rtp.NewDepacketizer(depacketizerTimeout, nil)
	discard := pipeline.NewDiscard[mrtp.EncodedFrame]()
	return discard, errors.Join(g.Connect(src, depacketizer), g.Connect(depacketizer, discard))
}
