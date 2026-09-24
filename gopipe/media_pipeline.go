//go:build cgo

package gopipe

import (
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"time"

	"github.com/mengelbart/mrtp"
	"github.com/mengelbart/mrtp/fake"
	"github.com/mengelbart/mrtp/media"
	"github.com/mengelbart/mrtp/packetization"
	"github.com/mengelbart/mrtp/pipeline"
)

func init() {
	media.Register("go", &implementation{})
}

const (
	// sendQueueDepth is how many RTP packets may wait to be sent before the
	// queue starts dropping whole frames.
	sendQueueDepth = 1000

	// sinkFPSNum and sinkFPSDen are the frame rate written into the header of a
	// received Y4M file.
	// TODO: this could be read from the media instead.
	sinkFPSNum = 30
	sinkFPSDen = 1

	// fakeFPS is the frame rate of the fake codec's source.
	fakeFPS = 30
)

type implementation struct {
	mtu                 uint
	depacketizerTimeout time.Duration
	fakeRunTime         time.Duration
}

// ConfigureFlags implements media.Implementation.
func (f *implementation) ConfigureFlags(fs *flag.FlagSet) {
	fs.UintVar(&f.mtu, "go-mtu", 1420,
		"Maximum size in bytes of the RTP packets the packetizer produces")
	fs.DurationVar(&f.depacketizerTimeout, "go-depacketizer-timeout", 150*time.Millisecond,
		"How long the depacketizer waits for a missing packet before giving up on the frame")
	fs.DurationVar(&f.fakeRunTime, "go-fake-run-time", 100*time.Second,
		fmt.Sprintf("How long the source of the %v codec keeps generating media", mrtp.Fake))
}

// NewFactory implements media.Implementation.
func (f *implementation) NewFactory() (media.Factory, error) {
	if f.mtu > math.MaxUint16 {
		return nil, fmt.Errorf("invalid -go-mtu value %v", f.mtu)
	}
	return &factory{impl: f}, nil
}

// factory builds gopipe graphs. Every stream is one graph of its own, because
// gopipe has nothing that shares state between streams.
type factory struct {
	impl *implementation
}

// Shared implements media.Factory. gopipe has no state outside a stream, so
// its shared graph is empty.
func (f *factory) Shared() *pipeline.Graph {
	return pipeline.NewGraph()
}

// NewSender implements media.Factory.
func (f *factory) NewSender(config media.SenderConfig) (*media.SendStream, error) {
	format, err := media.RTPFormat(config.Codec, config.PayloadType)
	if err != nil {
		return nil, err
	}
	packetizer := packetization.NewRTPPacketizer(
		uint16(f.impl.mtu),
		format.PayloadType,
		format.SSRC, // TODO: Set SSRC to a random value, or allow the user to set it.
		format.ClockRate,
		format.Codec,
	)

	g := pipeline.NewGraph()
	source, sender, err := f.newSource(g, config)
	if err != nil {
		return nil, err
	}

	if err := source.connect(g, packetizer); err != nil {
		return nil, err
	}
	rtp, err := sendTail(g, packetizer, source.frameDuration)
	if err != nil {
		return nil, err
	}
	g.Terminal(source.driver)

	return &media.SendStream{Graph: g, RTP: rtp, Sender: sender}, nil
}

// sendSource is the head of a send graph: the element that produces the coded
// frames the packetizer takes, whatever it took to get there.
type sendSource struct {
	driver mrtp.Driver
	// frameDuration is the interval the source produces frames at, which is
	// the window the packets of one frame are spread over.
	frameDuration time.Duration
	connect       func(*pipeline.Graph, mrtp.Sink[mrtp.EncodedFrame]) error
}

// newSource builds the head of a send graph, and the handle rate control
// steers it with.
func (f *factory) newSource(g *pipeline.Graph, config media.SenderConfig) (*sendSource, media.Sender, error) {
	if config.Codec == mrtp.Fake {
		// The fake codec generates its frames from the target bitrate instead
		// of encoding media, so it is a source of coded frames on its own.
		if config.SourceLocation != media.SourceTest {
			return nil, nil, fmt.Errorf("the %v codec generates its own media, it cannot send %q",
				mrtp.Fake, config.SourceLocation)
		}
		bounds := config.RateBounds
		if bounds.Max == 0 {
			return nil, nil, fmt.Errorf("the %v codec needs rate bounds, its frame sizes are its target bitrate", mrtp.Fake)
		}
		source, err := fake.New(f.impl.fakeRunTime, fakeFPS, bounds)
		if err != nil {
			return nil, nil, err
		}
		return &sendSource{
			driver:        source,
			frameDuration: source.FrameDuration(),
			connect: func(g *pipeline.Graph, down mrtp.Sink[mrtp.EncodedFrame]) error {
				return g.Connect(source, down)
			},
		}, source, nil
	}

	if config.SourceLocation == media.SourceTest {
		return nil, nil, errors.New("the go pipeline has no test source, pass a Y4M file to -source-location")
	}
	file, err := os.Open(config.SourceLocation)
	if err != nil {
		return nil, nil, err
	}
	source, err := NewY4MSource(file)
	if err != nil {
		return nil, nil, errors.Join(err, file.Close())
	}
	g.Add(file)

	encoder := NewEncoder(config.Codec)
	format := source.Format().(mrtp.RawVideo)
	return &sendSource{
		driver:        source,
		frameDuration: format.FrameRate.Duration(),
		connect: func(g *pipeline.Graph, down mrtp.Sink[mrtp.EncodedFrame]) error {
			return errors.Join(g.Connect(source, encoder), g.Connect(encoder, down))
		},
	}, encoder, nil
}

// sendTail spaces the packets of a frame out over the frame's duration, and
// returns the port the transport is wired to.
func sendTail(g *pipeline.Graph, packetizer *packetization.RTPPacketizer, frameDuration time.Duration) (mrtp.Source[mrtp.RTPPacket], error) {
	queue := pipeline.NewQueue(sendQueueDepth, pipeline.PaceFrames(
		(*mrtp.RTPPacket).Marker, frameDuration,
	))
	pump := pipeline.NewPump[mrtp.RTPPacket]()

	return pump, errors.Join(
		g.Connect(packetizer, queue),
		g.Attach(queue, pump),
	)
}

// NewReceiver implements media.Factory.
func (f *factory) NewReceiver(config media.ReceiverConfig) (*media.ReceiveStream, error) {
	// The depacketizer waits for a missing packet, so how long it is worth
	// waiting depends on the round trip time. A transport that does not know
	// its RTT leaves it at the fixed -go-depacketizer-timeout.
	depacketizer := packetization.NewRTPDepacketizer(f.impl.depacketizerTimeout, config.RTT)

	g := pipeline.NewGraph()
	if err := receiveTail(g, depacketizer, config); err != nil {
		return nil, err
	}
	return &media.ReceiveStream{Graph: g, RTP: depacketizer}, nil
}

// receiveTail wires the depacketizer to what the media is written to: a
// decoder and a Y4M file, or a sink that drops it.
func receiveTail(g *pipeline.Graph, depacketizer *packetization.RTPDepacketizer, config media.ReceiverConfig) error {
	if config.Codec == mrtp.Fake {
		// Fake frames carry no media, so there is nothing to decode, render or
		// write. Both the render and the discard location drop them.
		if config.SinkLocation != media.SinkDisplay && config.SinkLocation != media.SinkDiscard {
			return fmt.Errorf("the %v codec produces no media, it cannot be written to %q",
				mrtp.Fake, config.SinkLocation)
		}
		return g.Connect(depacketizer, pipeline.NewDiscard[mrtp.EncodedFrame]())
	}

	decoder, err := NewDecoder(config.Codec)
	if err != nil {
		return err
	}

	var sink mrtp.Sink[mrtp.RawFrame]
	switch config.SinkLocation {
	case media.SinkDisplay:
		return errors.New("the go pipeline cannot render, pass a Y4M file to -sink-location")
	case media.SinkDiscard:
		sink = pipeline.NewDiscard[mrtp.RawFrame]()
	default:
		sink, err = NewY4MSink(config.SinkLocation, sinkFPSNum, sinkFPSDen)
		if err != nil {
			return err
		}
	}
	return errors.Join(g.Connect(depacketizer, decoder), g.Connect(decoder, sink))
}
