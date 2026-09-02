package pipeline

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"

	"github.com/mengelbart/mrtp"
)

// payload is a stand-in for a media payload: a number to check ordering with
// and an access unit boundary.
type payload struct {
	n      int
	marker bool
}

func newPayloadPool() *Pool[payload] {
	return NewPool(
		func() *payload { return &payload{} },
		func(p *payload) { *p = payload{} },
	)
}

func boundary(p *payload) bool { return p.marker }

var (
	rawFormat     = mrtp.RawVideo{Width: 1280, Height: 720, FrameRate: mrtp.FrameRate{Num: 60, Den: 1}}
	encodedFormat = mrtp.EncodedVideo{Codec: mrtp.H264, Width: 1280, Height: 720}
)

// counter is a push source of n packets, numbered from 0, with a boundary
// every au packets.
type counter struct {
	pool   *Pool[payload]
	n      int
	au     int
	format mrtp.Format
	down   mrtp.Sink[payload]
}

func newCounter(pool *Pool[payload], n int) *counter {
	return &counter{pool: pool, n: n, au: 1, format: rawFormat}
}

func (c *counter) Format() mrtp.Format { return c.format }

func (c *counter) Connect(s mrtp.Sink[payload]) error {
	c.down = s
	return nil
}

func (c *counter) Run(context.Context) error {
	for i := range c.n {
		p := c.pool.Get()
		p.Value().n = i
		p.Value().marker = (i+1)%c.au == 0
		if err := c.down.Write(p); err != nil {
			return err
		}
	}
	return c.down.EndOfStream()
}

func (c *counter) Close() error { return nil }

// collector is a push sink that records what it was handed and releases it.
type collector struct {
	mu       sync.Mutex
	got      []int
	formats  []mrtp.Format
	eos      int
	closed   int
	reject   error
	writeErr error
}

func (c *collector) Negotiate(f mrtp.Format) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.reject != nil {
		return c.reject
	}
	c.formats = append(c.formats, f)
	return nil
}

func (c *collector) Write(p mrtp.Packet[payload]) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.got = append(c.got, p.Value().n)
	p.Release()
	return c.writeErr
}

func (c *collector) EndOfStream() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.eos++
	return nil
}

func (c *collector) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed++
	return nil
}

func (c *collector) values() []int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]int(nil), c.got...)
}

func (c *collector) format() mrtp.Format {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.formats) == 0 {
		return nil
	}
	return c.formats[0]
}

// doubler is a 1:1 push processor: it takes raw video, doubles every value and
// produces encoded video.
type doubler struct {
	pool *Pool[payload]
	down mrtp.Sink[payload]
	in   mrtp.RawVideo
}

func (d *doubler) Negotiate(f mrtp.Format) error {
	raw, ok := f.(mrtp.RawVideo)
	if !ok {
		return fmt.Errorf("doubler takes raw video, not %v", f)
	}
	d.in = raw
	return nil
}

func (d *doubler) Write(p mrtp.Packet[payload]) error {
	defer p.Release()
	out := d.pool.Get()
	out.Value().n = 2 * p.Value().n
	out.Value().marker = p.Value().marker
	return d.down.Write(out)
}

func (d *doubler) EndOfStream() error { return d.down.EndOfStream() }

func (d *doubler) Format() mrtp.Format {
	return mrtp.EncodedVideo{
		Codec:  mrtp.H264,
		Width:  d.in.Width,
		Height: d.in.Height,
	}
}

func (d *doubler) Connect(s mrtp.Sink[payload]) error {
	d.down = s
	return nil
}

func (d *doubler) Close() error { return nil }

// list is a pull source over a fixed set of values.
type list struct {
	pool   *Pool[payload]
	values []int
	format mrtp.Format
	i      int
}

func newList(pool *Pool[payload], values ...int) *list {
	return &list{pool: pool, values: values, format: encodedFormat}
}

func (l *list) Format() mrtp.Format { return l.format }

func (l *list) Pull(context.Context) (mrtp.Packet[payload], error) {
	if l.i >= len(l.values) {
		return nil, io.EOF
	}
	p := l.pool.Get()
	p.Value().n = l.values[l.i]
	l.i++
	return p, nil
}

func (l *list) Close() error { return nil }

// drain is a pull sink that pulls until the stream ends.
type drain struct {
	up     mrtp.Puller[payload]
	format mrtp.Format

	mu  sync.Mutex
	got []int
}

func (d *drain) Attach(p mrtp.Puller[payload]) error {
	d.up = p
	d.format = p.Format()
	return nil
}

func (d *drain) Run(ctx context.Context) error {
	for {
		p, err := d.up.Pull(ctx)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		d.mu.Lock()
		d.got = append(d.got, p.Value().n)
		d.mu.Unlock()
		p.Release()
	}
}

func (d *drain) Close() error { return nil }

func (d *drain) values() []int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]int(nil), d.got...)
}

// blocker is a driver that runs until its context is cancelled.
type blocker struct{}

func (blocker) Run(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

func (blocker) Close() error { return nil }

// must fails the test if wiring or configuring an element failed.
func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
