//go:build cgo

package gopipe

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"sync"
	"time"

	"github.com/mengelbart/mrtp"
	"github.com/mengelbart/mrtp/media"
	"github.com/mengelbart/mrtp/pipeline"
)

func init() {
	media.Register("go", &factory{})
}

const (
	// readBufferSize is the size of the buffer one RTP packet is read into.
	readBufferSize = math.MaxUint16

	// rtcpBufferSize is the size of the buffer one RTCP packet is read into.
	rtcpBufferSize = math.MaxUint16

	// sendQueueDepth is how many RTP packets may wait to be sent before the
	// queue starts dropping whole frames.
	sendQueueDepth = 1000

	// sinkFPSNum and sinkFPSDen are the frame rate written into the header of a
	// received Y4M file.
	// TODO: this could be read from the media instead.
	sinkFPSNum = 30
	sinkFPSDen = 1
)

type factory struct {
	mtu                 uint
	depacketizerTimeout time.Duration
	fakeRunTime         time.Duration
}

// ConfigureFlags implements media.Factory.
func (f *factory) ConfigureFlags(fs *flag.FlagSet) {
	fs.UintVar(&f.mtu, "go-mtu", 1420,
		"Maximum size in bytes of the RTP packets the packetizer produces")
	fs.DurationVar(&f.depacketizerTimeout, "go-depacketizer-timeout", 150*time.Millisecond,
		"How long the depacketizer waits for a missing packet before giving up on the frame")
	fs.DurationVar(&f.fakeRunTime, "go-fake-run-time", 100*time.Second,
		fmt.Sprintf("How long the source of the %v codec keeps generating media", mrtp.Fake))
}

// NewPipeline implements media.Factory.
func (f *factory) NewPipeline() (media.Pipeline, error) {
	if f.mtu > math.MaxUint16 {
		return nil, fmt.Errorf("invalid -go-mtu value %v", f.mtu)
	}
	return &mediaPipeline{factory: f, done: make(chan error, 1)}, nil
}

// mediaPipeline runs gopipe graphs as a media.Pipeline. Every stream is one
// graph driven by its own goroutine, because gopipe has nothing that shares
// state between streams.
type mediaPipeline struct {
	factory *factory

	mu sync.Mutex
	// ctx is nil until Run is called. Streams added before that wait in
	// pending, streams added afterwards start immediately.
	ctx     context.Context
	pending []*stream
	closers []io.Closer

	// done carries the first terminal event, see stream.terminal.
	done chan error
}

// stream is one running graph.
type stream struct {
	// run drives the graph until the media ends, ctx is cancelled, or an error
	// occurs.
	run func(context.Context) error

	// terminal marks a stream whose completion ends the pipeline: a sender is
	// done when its media ends, while a receiver only ends when it is
	// cancelled.
	terminal bool
}

// AddSender implements media.Pipeline.
func (p *mediaPipeline) AddSender(config media.SenderConfig) (media.Sender, error) {
	if config.RTP == nil {
		return nil, fmt.Errorf("stream %q has no RTP endpoint to send to", config.Name)
	}
	if config.PayloadType < 0 || config.PayloadType > math.MaxInt8 {
		return nil, fmt.Errorf("invalid payload type %v: the RTP payload type field is 7 bits, so it must be in [0, %v]", config.PayloadType, math.MaxInt8)
	}
	packetizer := NewRTPPacketizer(
		uint16(p.factory.mtu),
		uint8(config.PayloadType),
		0, // TODO: Set SSRC to a random value, or allow the user to set it.
		uint32(config.Codec.ClockRate()),
		config.Codec,
	)

	source, sender, err := p.newSource(config)
	if err != nil {
		return nil, err
	}

	g := pipeline.NewGraph()
	if err := errors.Join(
		source.connect(g, packetizer),
		p.sendTail(g, packetizer, config, source.frameDuration),
	); err != nil {
		return nil, err
	}
	g.Terminal(source.driver)

	p.addCloser(closerFunc(g.Close))
	p.drainRTCP(config.RTCP)
	p.addStream(&stream{terminal: true, run: g.Run})
	return sender, nil
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
func (p *mediaPipeline) newSource(config media.SenderConfig) (*sendSource, media.Sender, error) {
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
		source := NewFakeSource(p.factory.fakeRunTime, uint64(bounds.Min), uint64(bounds.Max), uint64(bounds.Initial))
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
	p.addCloser(file)

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

// sendTail wires the packetizer to the transport, spacing the packets of a
// frame out over the frame's duration on the way.
func (p *mediaPipeline) sendTail(g *pipeline.Graph, packetizer *RTPPacketizer, config media.SenderConfig, frameDuration time.Duration) error {
	queue := pipeline.NewQueue(sendQueueDepth, pipeline.PaceFrames(
		(*mrtp.RTPPacket).Marker, frameDuration,
	))
	pump := pipeline.NewPump[mrtp.RTPPacket]()
	out := pipeline.SinkFromWriter(nopWriteCloser{config.RTP}, rtpBytes)

	return errors.Join(
		g.Connect(packetizer, queue),
		g.Attach(queue, pump),
		g.Connect(pump, out),
	)
}

// AddReceiver implements media.Pipeline.
func (p *mediaPipeline) AddReceiver(config media.ReceiverConfig) error {
	if config.RTP == nil {
		return fmt.Errorf("stream %q has no RTP endpoint to receive from", config.Name)
	}
	source := pipeline.SourceFromReader(nopReadCloser{config.RTP}, mrtp.RTP{
		Codec:       config.Codec,
		PayloadType: uint8(config.PayloadType),
		ClockRate:   uint32(config.Codec.ClockRate()),
	}, readBufferSize, rtpBytes)

	// The depacketizer waits for a missing packet, so how long it is worth
	// waiting depends on the round trip time. A transport that does not know
	// its RTT leaves it at the fixed -go-depacketizer-timeout.
	rtt, _ := config.RTP.(mrtp.RTTSource)
	depacketizer := NewRTPDepacketizer(p.factory.depacketizerTimeout, rtt)

	g := pipeline.NewGraph()
	if err := g.Connect(source, depacketizer); err != nil {
		return err
	}
	if err := p.receiveTail(g, depacketizer, config); err != nil {
		return err
	}

	p.addCloser(closerFunc(g.Close))
	p.drainRTCP(config.RTCP)
	p.addStream(&stream{run: g.Run})
	return nil
}

// receiveTail wires the depacketizer to what the media is written to: a
// decoder and a Y4M file, or a sink that drops it.
func (p *mediaPipeline) receiveTail(g *pipeline.Graph, depacketizer *RTPDepacketizer, config media.ReceiverConfig) error {
	if config.Codec == mrtp.Fake {
		// Fake frames carry no media, so there is nothing to decode, render or
		// write. Both the render and the discard location drop them.
		if config.SinkLocation != media.SinkDisplay && config.SinkLocation != media.SinkDiscard {
			return fmt.Errorf("the %v codec produces no media, it cannot be written to %q",
				mrtp.Fake, config.SinkLocation)
		}
		return g.Connect(depacketizer, NewDiscardSink[mrtp.EncodedFrame]())
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
		sink = NewDiscardSink[mrtp.RawFrame]()
	default:
		sink, err = NewY4MSink(config.SinkLocation, sinkFPSNum, sinkFPSDen)
		if err != nil {
			return err
		}
	}
	return errors.Join(g.Connect(depacketizer, decoder), g.Connect(decoder, sink))
}

// drainRTCP reads the RTCP the peer sends and drops it. gopipe neither
// generates RTCP nor has a use for the reports, but reading them is what lets
// the transport's interceptors see the feedback the congestion controller runs
// on.
//
// It is not part of the stream's graph: an RTCP read failing must not take the
// media down with it.
func (p *mediaPipeline) drainRTCP(flow media.RTCPFlow) {
	if flow.Recv == nil {
		return
	}
	p.addStream(&stream{
		run: func(ctx context.Context) error {
			buf := make([]byte, rtcpBufferSize)
			for {
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
				}
				if _, err := flow.Recv.Read(buf); err != nil {
					return err
				}
			}
		},
	})
}

// Run implements media.Pipeline.
func (p *mediaPipeline) Run(ctx context.Context) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	p.mu.Lock()
	if p.ctx != nil {
		p.mu.Unlock()
		return errors.New("pipeline is already running")
	}
	p.ctx = runCtx
	pending := p.pending
	p.pending = nil
	p.mu.Unlock()

	for _, s := range pending {
		p.launch(runCtx, s)
	}

	select {
	case err := <-p.done:
		return err
	case <-runCtx.Done():
		return nil
	}
}

// Close implements media.Pipeline.
func (p *mediaPipeline) Close() error {
	p.mu.Lock()
	closers := p.closers
	p.closers = nil
	p.mu.Unlock()

	var err error
	for _, c := range closers {
		err = errors.Join(err, c.Close())
	}
	return err
}

// addStream starts a stream, or queues it until Run starts.
func (p *mediaPipeline) addStream(s *stream) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ctx == nil {
		p.pending = append(p.pending, s)
		return
	}
	p.launch(p.ctx, s)
}

// launch drives one stream, reporting the first terminal event to Run.
func (p *mediaPipeline) launch(ctx context.Context, s *stream) {
	go func() {
		err := s.run(ctx)
		if errors.Is(err, context.Canceled) {
			// The pipeline is shutting down, which is not a failure.
			err = nil
		}
		if err == nil && !s.terminal {
			return
		}
		select {
		case p.done <- err:
		default:
			// Run is already returning with an earlier event.
		}
	}()
}

func (p *mediaPipeline) addCloser(c io.Closer) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closers = append(p.closers, c)
}

// rtpBytes says where an RTP packet keeps its buffer, for the io adapters.
func rtpBytes(p *mrtp.RTPPacket) *[]byte {
	return &p.Data
}

// The RTP endpoints belong to the transport that handed them over, so the
// graph reads and writes them but does not close them.

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

type nopReadCloser struct{ io.Reader }

func (nopReadCloser) Close() error { return nil }

type closerFunc func() error

func (f closerFunc) Close() error { return f() }
