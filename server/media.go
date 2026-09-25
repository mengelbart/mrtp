package server

import (
	"errors"
	"math/rand/v2"

	"github.com/mengelbart/mrtp"
	"github.com/mengelbart/mrtp/element/fake"
	"github.com/mengelbart/mrtp/element/rtp"
	"github.com/mengelbart/mrtp/pipeline"
)

// Fake media a session sends until it closes.
const (
	fakeBitrate = 1_000_000
	fakeFPS     = 30
	fakeMTU     = 1200
)

// newFakeSender returns a graph that sends fake video to sink. The graph owns
// sink, and sink is closed if it fails.
func newFakeSender(sink mrtp.Sink[mrtp.RTPPacket]) (*pipeline.Graph, error) {
	source, err := fake.New(0, fakeFPS, mrtp.RateBounds{Initial: fakeBitrate, Min: fakeBitrate, Max: fakeBitrate})
	if err != nil {
		return nil, errors.Join(err, sink.Close())
	}
	format, err := mrtp.NewRTPFormat(mrtp.Fake, mrtp.DefaultPayloadType)
	if err != nil {
		return nil, errors.Join(err, sink.Close())
	}
	packetizer := rtp.NewPacketizer(fakeMTU, format.PayloadType, rand.Uint32(), format.ClockRate, format.Codec)
	g := pipeline.NewGraph()
	if err = errors.Join(g.Connect(source, packetizer), g.Connect(packetizer, sink)); err != nil {
		return nil, errors.Join(err, g.Close())
	}
	return g, nil
}
