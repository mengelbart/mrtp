//go:build cgo

package subcmd

import (
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"time"

	"github.com/mengelbart/mrtp"
	"github.com/mengelbart/mrtp/element/codec"
	"github.com/mengelbart/mrtp/element/fake"
	"github.com/mengelbart/mrtp/element/mediafile"
	"github.com/mengelbart/mrtp/element/rtp"
	"github.com/mengelbart/mrtp/pipeline"
)

const (
	// goSendQueueDepth is how many RTP packets may wait to be sent before the
	// queue starts dropping whole frames.
	goSendQueueDepth = 1000

	// goSinkFPSNum and goSinkFPSDen are the frame rate written into the header
	// of a received Y4M file.
	// TODO: this could be read from the media instead.
	goSinkFPSNum = 30
	goSinkFPSDen = 1

	// goFakeFPS is the frame rate of the fake codec's source.
	goFakeFPS = 30
)

// gopipeConfig is the configuration of the go media pipeline.
type gopipeConfig struct {
	mtu                 uint
	depacketizerTimeout time.Duration
	fakeRunTime         time.Duration
}

func (c *gopipeConfig) configureFlags(fs *flag.FlagSet) {
	fs.UintVar(&c.mtu, "go-mtu", 1200,
		"Maximum size in bytes of the RTP packets the packetizer produces")
	fs.DurationVar(&c.depacketizerTimeout, "go-depacketizer-timeout", 150*time.Millisecond,
		"How long the depacketizer waits for a missing packet before giving up on the frame")
	fs.DurationVar(&c.fakeRunTime, "go-fake-run-time", 100*time.Second,
		fmt.Sprintf("How long the source of the %v codec keeps generating media", mrtp.Fake))
}

// goPipeline builds streams from Go elements. Streams share no state.
type goPipeline struct {
	config *gopipeConfig
}

func newGoPipeline(config *gopipeConfig) (*goPipeline, error) {
	if config.mtu > math.MaxUint16 {
		return nil, fmt.Errorf("invalid -go-mtu value %v", config.mtu)
	}
	return &goPipeline{config: config}, nil
}

func (p *goPipeline) addSender(g *pipeline.Graph, config senderConfig, t sendEndpoints) (mrtp.TargetBitrateSetter, error) {
	if t.rtp == nil {
		return nil, errGstUDPWithoutPipeline
	}
	format, err := mrtp.NewRTPFormat(config.codec, config.payloadType)
	if err != nil {
		return nil, err
	}
	packetizer := rtp.NewPacketizer(
		uint16(p.config.mtu),
		format.PayloadType,
		format.SSRC, // TODO: Set SSRC to a random value, or allow the user to set it.
		format.ClockRate,
		format.Codec,
	)

	source, sender, err := p.newSource(g, config)
	if err != nil {
		return nil, err
	}
	queue := pipeline.NewQueue(goSendQueueDepth, pipeline.PaceFrames(
		(*mrtp.RTPPacket).Marker, source.frameDuration,
	))
	pump := pipeline.NewPump[mrtp.RTPPacket]()
	if err = errors.Join(
		source.connect(g, packetizer),
		g.Connect(packetizer, queue),
		g.Attach(queue, pump),
		g.Connect(pump, t.rtp),
		connectRTCP(g, nil, nil, t.rtcpEndpoints),
	); err != nil {
		return nil, err
	}
	g.Terminal(source.driver)
	return sender, nil
}

// goSendSource is the head of a send graph: the element that produces the
// coded frames the packetizer takes, whatever it took to get there.
type goSendSource struct {
	driver mrtp.Driver
	// frameDuration is the interval the source produces frames at, which is
	// the window the packets of one frame are spread over.
	frameDuration time.Duration
	connect       func(*pipeline.Graph, mrtp.Sink[mrtp.EncodedFrame]) error
}

// newSource builds the head of a send graph, and the handle rate control
// steers it with.
func (p *goPipeline) newSource(g *pipeline.Graph, config senderConfig) (*goSendSource, mrtp.TargetBitrateSetter, error) {
	if config.codec == mrtp.Fake {
		// The fake codec generates its frames from the target bitrate instead
		// of encoding media, so it is a source of coded frames on its own.
		if config.sourceLocation != sourceTest {
			return nil, nil, fmt.Errorf("the %v codec generates its own media, it cannot send %q",
				mrtp.Fake, config.sourceLocation)
		}
		bounds := config.rateBounds
		if bounds.Max == 0 {
			return nil, nil, fmt.Errorf("the %v codec needs rate bounds, its frame sizes are its target bitrate", mrtp.Fake)
		}
		source, err := fake.New(p.config.fakeRunTime, goFakeFPS, bounds)
		if err != nil {
			return nil, nil, err
		}
		return &goSendSource{
			driver:        source,
			frameDuration: source.FrameDuration(),
			connect: func(g *pipeline.Graph, down mrtp.Sink[mrtp.EncodedFrame]) error {
				return g.Connect(source, down)
			},
		}, source, nil
	}

	if config.sourceLocation == sourceTest {
		return nil, nil, errors.New("the go pipeline has no test source, pass a Y4M file to -source-location")
	}
	file, err := os.Open(config.sourceLocation)
	if err != nil {
		return nil, nil, err
	}
	source, err := mediafile.NewY4MSource(file)
	if err != nil {
		return nil, nil, errors.Join(err, file.Close())
	}
	g.Add(file)

	encoder := codec.NewEncoder(config.codec)
	format := source.Format().(mrtp.RawVideo)
	return &goSendSource{
		driver:        source,
		frameDuration: format.FrameRate.Duration(),
		connect: func(g *pipeline.Graph, down mrtp.Sink[mrtp.EncodedFrame]) error {
			return errors.Join(g.Connect(source, encoder), g.Connect(encoder, down))
		},
	}, encoder, nil
}

func (p *goPipeline) addReceiver(g *pipeline.Graph, config receiverConfig, t receiveEndpoints) error {
	if t.rtp == nil {
		return errGstUDPWithoutPipeline
	}
	// The depacketizer waits for a missing packet, so how long it is worth
	// waiting depends on the round trip time. A transport that does not know
	// its RTT leaves it at the fixed -go-depacketizer-timeout.
	depacketizer := rtp.NewDepacketizer(p.config.depacketizerTimeout, t.rtt)
	return errors.Join(
		g.Connect(t.rtp, depacketizer),
		goReceiveTail(g, depacketizer, config),
		connectRTCP(g, nil, nil, t.rtcpEndpoints),
	)
}

// goReceiveTail wires the depacketizer to what the media is written to: a
// decoder and a Y4M file, or a sink that drops it.
func goReceiveTail(g *pipeline.Graph, depacketizer *rtp.Depacketizer, config receiverConfig) error {
	if config.codec == mrtp.Fake {
		// Fake frames carry no media, so there is nothing to decode, render or
		// write. Both the render and the discard location drop them.
		if config.sinkLocation != sinkDisplay && config.sinkLocation != sinkDiscard {
			return fmt.Errorf("the %v codec produces no media, it cannot be written to %q",
				mrtp.Fake, config.sinkLocation)
		}
		return g.Connect(depacketizer, pipeline.NewDiscard[mrtp.EncodedFrame]())
	}

	decoder, err := codec.NewDecoder(config.codec)
	if err != nil {
		return err
	}

	var sink mrtp.Sink[mrtp.RawFrame]
	switch config.sinkLocation {
	case sinkDisplay:
		return errors.New("the go pipeline cannot render, pass a Y4M file to -sink-location")
	case sinkDiscard:
		sink = pipeline.NewDiscard[mrtp.RawFrame]()
	default:
		sink, err = mediafile.NewY4MSink(config.sinkLocation, goSinkFPSNum, goSinkFPSDen)
		if err != nil {
			return err
		}
	}
	return errors.Join(g.Connect(depacketizer, decoder), g.Connect(decoder, sink))
}
