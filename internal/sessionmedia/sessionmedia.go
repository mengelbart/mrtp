// Package sessionmedia builds the pure Go media graphs of the signaling client
// and server: paced senders of Fake video or of an IVF file, and a receiver
// that depacketizes frames into a sink.
package sessionmedia

import (
	"errors"
	"math/rand/v2"
	"time"

	"github.com/mengelbart/mrtp"
	"github.com/mengelbart/mrtp/element/fake"
	"github.com/mengelbart/mrtp/element/mediafile"
	"github.com/mengelbart/mrtp/element/rtp"
	"github.com/mengelbart/mrtp/pipeline"
)

// FakeCodec is the codec of fake senders.
const FakeCodec = mrtp.Fake

const (
	// sendQueueDepth is how many RTP packets may wait to be sent before the
	// queue starts dropping whole frames.
	sendQueueDepth = 1000
	// depacketizerTimeout is how long a receiver waits for a missing packet
	// before giving up on the frame.
	depacketizerTimeout = 150 * time.Millisecond
)

// FakeConfig configures a fake sender.
type FakeConfig struct {
	// Duration is how long the sender sends, 0 for no limit.
	Duration time.Duration
	FPS      uint64
	// MTU is the maximum RTP packet size in bytes.
	MTU    uint16
	Bounds mrtp.RateBounds
}

// AddFakeSender wires Fake video to sink into g. It returns the source rate
// control steers.
func AddFakeSender(g *pipeline.Graph, config FakeConfig, sink mrtp.Sink[mrtp.RTPPacket]) (*fake.Source, error) {
	g.Add(sink)
	source, err := fake.New(config.Duration, config.FPS, config.Bounds)
	if err != nil {
		return nil, err
	}
	packetizer, err := addPacketizer(g, FakeCodec, config.MTU, source.FrameDuration(), sink)
	if err != nil {
		return nil, err
	}
	return source, g.Connect(source, packetizer)
}

// AddFileSender wires the frames of source to sink into g, at the pace of
// their timestamps.
func AddFileSender(g *pipeline.Graph, source *mediafile.IVFSource, mtu uint16, sink mrtp.Sink[mrtp.RTPPacket]) error {
	g.Add(source)
	g.Add(sink)
	packetizer, err := addPacketizer(g, source.Format().(mrtp.EncodedVideo).Codec, mtu, source.FrameDuration(), sink)
	if err != nil {
		return err
	}
	pump := pipeline.NewPump[mrtp.EncodedFrame]()
	return errors.Join(g.Attach(source, pump), g.Connect(pump, packetizer))
}

// addPacketizer wires a packetizer to sink into g, with the packets of each
// frame spread over the frame's duration, and returns the packetizer. The
// graph ends once the last packet is sent.
func addPacketizer(g *pipeline.Graph, codec mrtp.Codec, mtu uint16, frameDuration time.Duration, sink mrtp.Sink[mrtp.RTPPacket]) (*rtp.Packetizer, error) {
	format, err := mrtp.NewRTPFormat(codec, mrtp.DefaultPayloadType)
	if err != nil {
		return nil, err
	}
	packetizer := rtp.NewPacketizer(mtu, format.PayloadType, rand.Uint32(), format.ClockRate, format.Codec)
	queue := pipeline.NewQueue(sendQueueDepth, pipeline.PaceFrames((*mrtp.RTPPacket).Marker, frameDuration))
	pump := pipeline.NewPump[mrtp.RTPPacket]()
	g.Terminal(pump)
	return packetizer, errors.Join(
		g.Connect(packetizer, queue),
		g.Attach(queue, pump),
		g.Connect(pump, sink),
	)
}

// AddReceiver wires src through a depacketizer into sink.
func AddReceiver(g *pipeline.Graph, src mrtp.Source[mrtp.RTPPacket], sink mrtp.Sink[mrtp.EncodedFrame]) error {
	depacketizer := rtp.NewDepacketizer(depacketizerTimeout, nil)
	return errors.Join(g.Connect(src, depacketizer), g.Connect(depacketizer, sink))
}
