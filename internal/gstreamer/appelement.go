//go:build cgo

package gstreamer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"

	"github.com/go-gst/go-gst/gst"
	"github.com/go-gst/go-gst/gst/app"
	"github.com/mengelbart/mrtp"
	"github.com/mengelbart/mrtp/pipeline"
)

func newAppSinkSource[T any](f mrtp.Format, bytes func(*T) *[]byte) (*appSinkSource[T], error) {
	element, err := gst.NewElementWithProperties(
		"appsink",
		map[string]any{
			"async": false,
			"sync":  false,
		},
	)
	if err != nil {
		return nil, err
	}
	s := &appSinkSource[T]{
		element: element,
		format:  f,
		bytes:   bytes,
		pool: pipeline.NewPool(
			func() *T {
				var value T
				return &value
			},
			func(value *T) {
				buffer := bytes(value)
				*buffer = (*buffer)[:0]
			},
		),
		eos: make(chan struct{}),
	}
	app.SinkFromElement(element).SetCallbacks(&app.SinkCallbacks{
		EOSFunc:       func(*app.Sink) { s.endOfStream() },
		NewSampleFunc: s.newSample,
	})
	return s, nil
}

// appSinkSource is an appsink as an [mrtp.Source]. GStreamer's streaming thread
// pushes the packets, so Run only waits for the end of the stream.
type appSinkSource[T any] struct {
	element *gst.Element
	format  mrtp.Format
	bytes   func(*T) *[]byte
	pool    *pipeline.Pool[T]

	lock sync.Mutex
	down mrtp.Sink[T]

	eos     chan struct{}
	eosOnce sync.Once
}

// Format implements mrtp.Source.
func (s *appSinkSource[T]) Format() mrtp.Format {
	return s.format
}

// Connect implements mrtp.Source.
func (s *appSinkSource[T]) Connect(down mrtp.Sink[T]) error {
	s.lock.Lock()
	defer s.lock.Unlock()
	if s.down != nil {
		return errors.New("gstreamer: appsink source is already connected")
	}
	s.down = down
	return nil
}

// Run implements mrtp.Driver. It waits for the end of the stream, which the
// streaming thread reports, and passes it on.
func (s *appSinkSource[T]) Run(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.eos:
	}
	s.lock.Lock()
	down := s.down
	s.lock.Unlock()
	if down == nil {
		return nil
	}
	return down.EndOfStream()
}

// Close implements mrtp.Element. The bin owns the gst element, so closing the
// source only unblocks Run.
func (s *appSinkSource[T]) Close() error {
	s.endOfStream()
	return nil
}

func (s *appSinkSource[T]) endOfStream() {
	s.eosOnce.Do(func() { close(s.eos) })
}

func (s *appSinkSource[T]) newSample(sink *app.Sink) gst.FlowReturn {
	sample := sink.PullSample()
	if sample == nil {
		return gst.FlowEOS
	}
	buffer := sample.GetBuffer()
	if buffer == nil {
		return gst.FlowEOS
	}
	data := buffer.Map(gst.MapRead).AsUint8Slice()
	defer buffer.Unmap()

	s.lock.Lock()
	down := s.down
	s.lock.Unlock()
	if down == nil {
		slog.Warn("dropping packet from an unconnected appsink source")
		return gst.FlowOK
	}

	packet := s.pool.Get()
	payload := s.bytes(packet.Value())
	*payload = append((*payload)[:0], data...)
	if err := down.Write(packet); err != nil {
		slog.Error("failed to write packet from appsink source", "error", err)
		return gst.FlowError
	}
	return gst.FlowOK
}

// newAppSrc builds the appsrc both pushing shapes share.
func newAppSrc() (*gst.Element, *app.Source, error) {
	element, err := gst.NewElementWithProperties(
		"appsrc",
		map[string]any{
			"format": 3,
			"block":  true, // makes a full queue back pressure the producer instead of growing without bound.
		},
	)
	if err != nil {
		return nil, nil, err
	}
	src := app.SrcFromElement(element)
	src.SetStreamType(app.AppStreamTypeStream)
	return element, src, nil
}

// pushPacket hands one packet to an appsrc and releases it.
func pushPacket[T any](src *app.Source, bytes func(*T) *[]byte, packet mrtp.Packet[T]) error {
	defer packet.Release()
	data := *bytes(packet.Value())
	buffer := gst.NewBufferWithSize(int64(len(data)))
	buffer.Map(gst.MapWrite).WriteData(data)
	buffer.Unmap()
	switch ret := src.PushBuffer(buffer); ret {
	case gst.FlowOK:
		return nil
	case gst.FlowEOS, gst.FlowFlushing:
		return io.EOF
	default:
		return fmt.Errorf("gstreamer: appsrc rejected a packet: %v", ret)
	}
}

// endAppSrcStream ends an appsrc's stream.
func endAppSrcStream(src *app.Source) error {
	switch ret := src.EndStream(); ret {
	case gst.FlowOK, gst.FlowEOS, gst.FlowFlushing:
		return nil
	default:
		return fmt.Errorf("gstreamer: failed to end appsrc stream: %v", ret)
	}
}

// newAppSrcSink wraps an appsrc as a pushing input of the bin, one buffer per
// packet.
func newAppSrcSink[T any](negotiate func(mrtp.Format) error, bytes func(*T) *[]byte) (*appSrcSink[T], error) {
	element, src, err := newAppSrc()
	if err != nil {
		return nil, err
	}
	return &appSrcSink[T]{
		element:   element,
		src:       src,
		bytes:     bytes,
		negotiate: negotiate,
	}, nil
}

// appSrcSink is an appsrc as an [mrtp.Sink]. Its Write blocks while the appsrc
// queue is full, which is what carries the bin's back pressure to the transport.
type appSrcSink[T any] struct {
	element   *gst.Element
	src       *app.Source
	bytes     func(*T) *[]byte
	negotiate func(mrtp.Format) error
	ended     sync.Once
}

// Negotiate implements mrtp.Sink.
func (s *appSrcSink[T]) Negotiate(f mrtp.Format) error {
	return s.negotiate(f)
}

// Write implements mrtp.Sink.
func (s *appSrcSink[T]) Write(p mrtp.Packet[T]) error {
	return pushPacket(s.src, s.bytes, p)
}

// EndOfStream implements mrtp.Sink.
func (s *appSrcSink[T]) EndOfStream() error {
	return s.endStream()
}

// Close implements mrtp.Element.
func (s *appSrcSink[T]) Close() error {
	return s.endStream()
}

func (s *appSrcSink[T]) endStream() error {
	var err error
	s.ended.Do(func() { err = endAppSrcStream(s.src) })
	return err
}

// newAppSrcConsumer wraps an appsrc as a pulling input of the bin: it reads one
// packet per need-data, so the bin's demand is what drives the upstream.
func newAppSrcConsumer[T any](ctx context.Context, bytes func(*T) *[]byte) (*appSrcConsumer[T], error) {
	element, src, err := newAppSrc()
	if err != nil {
		return nil, err
	}
	c := &appSrcConsumer[T]{
		element: element,
		src:     src,
		bytes:   bytes,
		ctx:     ctx,
	}
	src.SetCallbacks(&app.SourceCallbacks{
		NeedDataFunc: func(*app.Source, uint) { c.needData() },
	})
	return c, nil
}

// appSrcConsumer is an appsrc as an [mrtp.Consumer].
type appSrcConsumer[T any] struct {
	element *gst.Element
	src     *app.Source
	bytes   func(*T) *[]byte
	ctx     context.Context

	// lock guards up, which the graph writes while wiring and the streaming
	// thread reads.
	lock  sync.Mutex
	up    mrtp.Puller[T]
	ended sync.Once
}

// Attach implements mrtp.Consumer.
func (c *appSrcConsumer[T]) Attach(up mrtp.Puller[T]) error {
	c.lock.Lock()
	defer c.lock.Unlock()
	if c.up != nil {
		return errors.New("gstreamer: appsrc consumer is already attached")
	}
	c.up = up
	return nil
}

// Close implements mrtp.Element.
func (c *appSrcConsumer[T]) Close() error {
	return c.endStream()
}

func (c *appSrcConsumer[T]) needData() {
	c.lock.Lock()
	up := c.up
	c.lock.Unlock()
	if up == nil {
		return
	}
	packet, err := up.Pull(c.ctx)
	if err != nil {
		if !errors.Is(err, io.EOF) && !errors.Is(err, context.Canceled) {
			slog.Error("failed to pull packet into appsrc", "error", err)
		}
		if err := c.endStream(); err != nil {
			slog.Error("failed to end appsrc stream", "error", err)
		}
		return
	}
	if err := pushPacket(c.src, c.bytes, packet); err != nil {
		if !errors.Is(err, io.EOF) {
			slog.Error("failed to push packet into appsrc", "error", err)
		}
		if err := c.endStream(); err != nil {
			slog.Error("failed to end appsrc stream", "error", err)
		}
	}
}

func (c *appSrcConsumer[T]) endStream() error {
	var err error
	c.ended.Do(func() { err = endAppSrcStream(c.src) })
	return err
}

var (
	_ mrtp.Source[mrtp.RTPPacket]    = (*appSinkSource[mrtp.RTPPacket])(nil)
	_ mrtp.Driver                    = (*appSinkSource[mrtp.RTPPacket])(nil)
	_ mrtp.Sink[mrtp.RTPPacket]      = (*appSrcSink[mrtp.RTPPacket])(nil)
	_ mrtp.Consumer[mrtp.RTCPPacket] = (*appSrcConsumer[mrtp.RTCPPacket])(nil)
)
