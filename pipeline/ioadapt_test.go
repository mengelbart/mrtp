package pipeline

import (
	"context"
	"errors"
	"io"
	"slices"
	"testing"

	"github.com/mengelbart/mrtp"
)

// chunkBytes is where a data chunk keeps its buffer.
func chunkBytes(c *mrtp.DataChunk) *[]byte { return &c.Data }

// packetWriter records what it was written, one entry per Write.
type packetWriter struct {
	got    [][]byte
	closed int
}

func (w *packetWriter) Write(p []byte) (int, error) {
	w.got = append(w.got, slices.Clone(p))
	return len(p), nil
}

func (w *packetWriter) Close() error {
	w.closed++
	return nil
}

// packetReader hands out one packet per Read, then ends.
type packetReader struct {
	packets [][]byte
	i       int
	closed  int
}

func (r *packetReader) Read(p []byte) (int, error) {
	if r.i >= len(r.packets) {
		return 0, io.EOF
	}
	n := copy(p, r.packets[r.i])
	r.i++
	return n, nil
}

func (r *packetReader) Close() error {
	r.closed++
	return nil
}

// chunkCollector is a push sink of data chunks.
type chunkCollector struct {
	got [][]byte
	eos int
}

func (c *chunkCollector) Negotiate(mrtp.Format) error { return nil }

func (c *chunkCollector) Write(p mrtp.Packet[mrtp.DataChunk]) error {
	c.got = append(c.got, slices.Clone(p.Value().Data))
	p.Release()
	return nil
}

func (c *chunkCollector) EndOfStream() error {
	c.eos++
	return nil
}

func (c *chunkCollector) Close() error { return nil }

func TestSinkFromWriterWritesOnePacketPerCall(t *testing.T) {
	w := &packetWriter{}
	sink := SinkFromWriter(w, chunkBytes)
	// A writer takes bytes, so it takes any format.
	must(t, sink.Negotiate(mrtp.Data{}))

	pool := NewPool(func() *mrtp.DataChunk { return &mrtp.DataChunk{} }, nil)
	for _, data := range [][]byte{{1, 2}, {3}} {
		p := pool.Get()
		p.Value().Data = data
		must(t, sink.Write(p))
	}
	if len(w.got) != 2 || !slices.Equal(w.got[0], []byte{1, 2}) || !slices.Equal(w.got[1], []byte{3}) {
		t.Fatalf("the writer got %v", w.got)
	}
	if pool.Outstanding() != 0 {
		t.Fatal("the sink did not release what it was handed")
	}
	must(t, sink.Close())
	if w.closed != 1 {
		t.Fatalf("the writer was closed %v times, want 1", w.closed)
	}
}

func TestSourceFromReaderReadsOnePacketPerCall(t *testing.T) {
	r := &packetReader{packets: [][]byte{{1, 2, 3}, {4}}}
	src := SourceFromReader(r, mrtp.Data{}, 16, chunkBytes)
	sink := &chunkCollector{}

	g := NewGraph()
	must(t, g.Connect(src, sink))

	must(t, g.Run(context.Background()))
	if len(sink.got) != 2 || !slices.Equal(sink.got[0], []byte{1, 2, 3}) || !slices.Equal(sink.got[1], []byte{4}) {
		t.Fatalf("the sink got %v", sink.got)
	}
	if sink.eos != 1 {
		t.Fatalf("the sink saw %v ends of stream, want 1", sink.eos)
	}
	if src.Format() != mrtp.Format(mrtp.Data{}) {
		t.Fatalf("the source carries %v, want the format it was given", src.Format())
	}
	must(t, g.Close())
	if r.closed != 1 {
		t.Fatalf("the reader was closed %v times, want 1", r.closed)
	}
}

func TestSourceFromReaderReusesItsBuffers(t *testing.T) {
	r := &packetReader{packets: [][]byte{{1, 2, 3}, {4}}}
	src := SourceFromReader(r, mrtp.Data{}, 16, chunkBytes)
	must(t, src.Connect(&chunkCollector{}))
	must(t, src.(mrtp.Driver).Run(context.Background()))

	// Every packet the source read was released, so the pool holds them all
	// again with their buffers back at full length.
	source := src.(*readerSource[mrtp.DataChunk])
	if source.pool.Outstanding() != 0 {
		t.Fatalf("%v packets were never released", source.pool.Outstanding())
	}
	if p := source.pool.Get(); len(p.Value().Data) != 16 {
		t.Fatalf("a reused buffer is %v bytes long, want the full 16", len(p.Value().Data))
	}
}

func TestSourceFromReaderStopsOnCancel(t *testing.T) {
	src := SourceFromReader(&packetReader{packets: [][]byte{{1}}}, mrtp.Data{}, 16, chunkBytes)
	must(t, src.Connect(&chunkCollector{}))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := src.(mrtp.Driver).Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run returned %v, want the cancellation", err)
	}
}
