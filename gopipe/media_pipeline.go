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
)

func init() {
	media.Register("go", &factory{})
}

const (
	// readBufferSize is the size of the buffer one RTP packet is read into.
	readBufferSize = math.MaxUint16

	// rtcpBufferSize is the size of the buffer one RTCP packet is read into.
	rtcpBufferSize = math.MaxUint16

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
	return &pipeline{factory: f, done: make(chan error, 1)}, nil
}

// pipeline runs gopipe chains as a media.Pipeline. Every stream is one chain
// driven by its own goroutine, because gopipe has nothing that shares state
// between streams.
type pipeline struct {
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

// stream is one running chain.
type stream struct {
	// run drives the chain until the media ends, ctx is cancelled, or an error
	// occurs.
	run func(context.Context) error

	// terminal marks a stream whose completion ends the pipeline: a sender is
	// done when its media ends, while a receiver only ends when it is
	// cancelled.
	terminal bool
}

// AddSender implements media.Pipeline.
func (p *pipeline) AddSender(config media.SenderConfig) (media.Sender, error) {
	if config.RTP == nil {
		return nil, fmt.Errorf("stream %q has no RTP endpoint to send to", config.Name)
	}
	if config.PayloadType < 0 || config.PayloadType > math.MaxInt8 {
		return nil, fmt.Errorf("invalid payload type %v: the RTP payload type field is 7 bits, so it must be in [0, %v]", config.PayloadType, math.MaxInt8)
	}
	packetizer := &RTPPacketizerFactory{
		MTU:       uint16(p.factory.mtu),
		PT:        uint8(config.PayloadType),
		SSRC:      0, // TODO: Set SSRC to a random value, or allow the user to set it.
		ClockRate: uint32(config.Codec.ClockRate()),
		Codec:     config.Codec,
	}
	sink := WriterFunc(func(b []byte, _ Attributes) error {
		_, err := config.RTP.Write(b)
		return err
	})

	p.drainRTCP(config.RTCP)

	if config.Codec == mrtp.Fake {
		return p.addFakeSender(config, sink, packetizer)
	}
	return p.addEncodedSender(config, sink, packetizer)
}

// addFakeSender adds a sender of the Fake codec, which generates its packets
// from the target bitrate instead of encoding media.
func (p *pipeline) addFakeSender(config media.SenderConfig, sink Sink, packetizer *RTPPacketizerFactory) (media.Sender, error) {
	if config.SourceLocation != media.SourceTest {
		return nil, fmt.Errorf("the %v codec generates its own media, it cannot send %q",
			mrtp.Fake, config.SourceLocation)
	}
	bounds := config.RateBounds
	if bounds.Max == 0 {
		return nil, fmt.Errorf("the %v codec needs rate bounds, its frame sizes are its target bitrate", mrtp.Fake)
	}
	source := NewFakeSource(p.factory.fakeRunTime, uint64(bounds.Min), uint64(bounds.Max), uint64(bounds.Initial))
	p.addCloser(source)

	p.addStream(&stream{
		terminal: true,
		run: func(ctx context.Context) error {
			chain, err := p.chain(ctx, source.GetInfo(), sink, packetizer)
			if err != nil {
				return err
			}
			return source.StartLive(ctx, chain)
		},
	})
	return source, nil
}

// addEncodedSender adds a sender that encodes the Y4M file at the config's
// source location.
func (p *pipeline) addEncodedSender(config media.SenderConfig, sink Sink, packetizer *RTPPacketizerFactory) (media.Sender, error) {
	if config.SourceLocation == media.SourceTest {
		return nil, errors.New("the go pipeline has no test source, pass a Y4M file to -source-location")
	}
	file, err := os.Open(config.SourceLocation)
	if err != nil {
		return nil, err
	}
	source, err := NewY4MSource(file)
	if err != nil {
		return nil, errors.Join(err, file.Close())
	}
	p.addCloser(file)

	encoder := NewEncoder(config.Codec)
	p.addCloser(encoder)

	p.addStream(&stream{
		terminal: true,
		run: func(ctx context.Context) error {
			chain, err := p.chain(ctx, source.GetInfo(), sink, packetizer, encoder)
			if err != nil {
				return err
			}
			return source.StartLive(ctx, chain)
		},
	})
	return encoder, nil
}

// chain builds the outgoing chain, spacing the packets of a frame out over the
// frame's duration. The spacer is built here rather than in AddSender because
// it needs the context that Run is driven with.
func (p *pipeline) chain(ctx context.Context, info Info, sink Sink, processors ...Processor) (Sink, error) {
	spacer := NewFrameSpacer(ctx)
	p.addCloser(spacer)
	return Chain(info, sink, append([]Processor{spacer}, processors...)...)
}

// AddReceiver implements media.Pipeline.
func (p *pipeline) AddReceiver(config media.ReceiverConfig) error {
	if config.RTP == nil {
		return fmt.Errorf("stream %q has no RTP endpoint to receive from", config.Name)
	}
	sink, decoders, err := p.newSink(config)
	if err != nil {
		return err
	}
	depacketizer, err := NewRTPDepacketizer(p.factory.depacketizerTimeout, config.Codec)
	if err != nil {
		return err
	}
	p.addCloser(depacketizer)
	p.drainRTCP(config.RTCP)

	p.addStream(&stream{
		run: func(ctx context.Context) error {
			chain, err := Chain(Info{}, sink, append(decoders, depacketizer)...)
			if err != nil {
				return err
			}
			buf := make([]byte, readBufferSize)
			for {
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
				}
				n, err := config.RTP.Read(buf)
				if err != nil {
					return err
				}
				if err = chain.Write(buf[:n], Attributes{}); err != nil {
					return err
				}
			}
		},
	})
	return nil
}

// drainRTCP reads the RTCP the peer sends and drops it. gopipe neither
// generates RTCP nor has a use for the reports, but reading them is what lets
// the transport's interceptors see the feedback the congestion controller runs
// on.
func (p *pipeline) drainRTCP(flow media.RTCPFlow) {
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

// newSink builds the end of an incoming chain: the sink itself, plus the
// processors that turn encoded frames into what the sink takes.
func (p *pipeline) newSink(config media.ReceiverConfig) (Sink, []Processor, error) {
	if config.Codec == mrtp.Fake {
		// Fake frames carry no media, so there is nothing to decode, render or
		// write. Both the render and the discard location drop them.
		if config.SinkLocation != media.SinkDisplay && config.SinkLocation != media.SinkDiscard {
			return nil, nil, fmt.Errorf("the %v codec produces no media, it cannot be written to %q",
				mrtp.Fake, config.SinkLocation)
		}
		sink, err := NewFakeSink()
		if err != nil {
			return nil, nil, err
		}
		p.addCloser(sink)
		return sink, nil, nil
	}

	decoder, err := NewDecoder(config.Codec)
	if err != nil {
		return nil, nil, err
	}
	p.addCloser(decoder)

	switch config.SinkLocation {
	case media.SinkDisplay:
		return nil, nil, errors.New("the go pipeline cannot render, pass a Y4M file to -sink-location")
	case media.SinkDiscard:
		sink, err := NewFakeSink()
		if err != nil {
			return nil, nil, err
		}
		p.addCloser(sink)
		return sink, []Processor{decoder}, nil
	default:
		sink, err := NewY4MSink(config.SinkLocation, sinkFPSNum, sinkFPSDen)
		if err != nil {
			return nil, nil, err
		}
		p.addCloser(sink)
		return sink, []Processor{decoder}, nil
	}
}

// Run implements media.Pipeline.
func (p *pipeline) Run(ctx context.Context) error {
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
func (p *pipeline) Close() error {
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
func (p *pipeline) addStream(s *stream) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ctx == nil {
		p.pending = append(p.pending, s)
		return
	}
	p.launch(p.ctx, s)
}

// launch drives one stream, reporting the first terminal event to Run.
func (p *pipeline) launch(ctx context.Context, s *stream) {
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

func (p *pipeline) addCloser(c io.Closer) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closers = append(p.closers, c)
}
