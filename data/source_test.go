package data

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mengelbart/mrtp"
	"github.com/mengelbart/mrtp/media"
	"github.com/mengelbart/mrtp/synthetic"
)

// chunkCollector keeps a copy of every chunk it is written and hands it on to
// a Sink, which checks the framing.
type chunkCollector struct {
	sink   Sink
	chunks [][]byte
	eos    int
}

func (c *chunkCollector) Negotiate(f mrtp.Format) error { return c.sink.Negotiate(f) }
func (c *chunkCollector) EndOfStream() error            { c.eos++; return nil }
func (c *chunkCollector) Close() error                  { return nil }

func (c *chunkCollector) Write(p mrtp.Packet[mrtp.DataChunk]) error {
	c.chunks = append(c.chunks, bytes.Clone(p.Value().Data))
	return c.sink.Write(p)
}

func header(size uint64) []byte {
	return binary.BigEndian.AppendUint64(nil, size)
}

func TestSourceChunks(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		config := ChunkConfig()
		config.Burst.Bytes = 2500
		config.Burst.Count = 2
		src, err := NewSource(config)
		if err != nil {
			t.Fatal(err)
		}
		c := &chunkCollector{}
		if err = src.Connect(c); err != nil {
			t.Fatal(err)
		}
		if err = src.Run(context.Background()); err != nil {
			t.Fatal(err)
		}

		if c.eos != 1 {
			t.Fatalf("got %v ends of stream, want 1", c.eos)
		}
		// two chunks of 2500 bytes, each written as 1000, 1000 and 500 bytes,
		// with the header in front of the first write
		wantSizes := []int{8 + 1000, 1000, 500, 8 + 1000, 1000, 500}
		if len(c.chunks) != len(wantSizes) {
			t.Fatalf("got %v writes, want %v", len(c.chunks), len(wantSizes))
		}
		for i, chunk := range c.chunks {
			if len(chunk) != wantSizes[i] {
				t.Errorf("write %v has %v bytes, want %v", i, len(chunk), wantSizes[i])
			}
			if i%3 == 0 && !bytes.Equal(chunk[:8], header(2500)) {
				t.Errorf("write %v starts with %x, want the header of 2500", i, chunk[:8])
			}
		}
		if c.sink.currentChunk != 2 || c.sink.inChunk {
			t.Errorf("sink counted %v chunks, inChunk=%v, want 2 complete chunks", c.sink.currentChunk, c.sink.inChunk)
		}
	})
}

func TestSourceStream(t *testing.T) {
	config := StreamConfig()
	src, err := NewSource(config)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	c := &chunkCollector{}
	counting := &cancelAfter{chunkCollector: c, n: 3, cancel: cancel}
	if err = src.Connect(counting); err != nil {
		t.Fatal(err)
	}
	if err = src.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
	if c.eos != 0 {
		t.Fatalf("got %v ends of stream on cancel, want 0", c.eos)
	}
	if len(c.chunks) != 3 {
		t.Fatalf("got %v writes, want 3", len(c.chunks))
	}
	if !bytes.Equal(c.chunks[0][:8], header(0)) || len(c.chunks[0]) != 8+1024 {
		t.Errorf("first write is %v bytes starting with %x, want a 0 header and 1024 bytes", len(c.chunks[0]), c.chunks[0][:8])
	}
	for i, chunk := range c.chunks[1:] {
		if len(chunk) != 1024 {
			t.Errorf("write %v has %v bytes, want 1024", i+1, len(chunk))
		}
	}
	if !c.sink.unbounded {
		t.Error("sink did not see a chunk without end")
	}
}

type cancelAfter struct {
	*chunkCollector
	n      int
	cancel context.CancelFunc
}

func (c *cancelAfter) Write(p mrtp.Packet[mrtp.DataChunk]) error {
	err := c.chunkCollector.Write(p)
	if len(c.chunks) == c.n {
		c.cancel()
	}
	return err
}

func TestSourceNeedsSizePacing(t *testing.T) {
	_, err := NewSource(synthetic.Config{
		Pacing:    synthetic.Interval,
		FrameRate: mrtp.FrameRate{Num: 1, Den: 1},
		Bounds:    media.RateBounds{Max: 1},
	})
	if err == nil {
		t.Fatal("got no error for interval pacing")
	}
}

func TestFileSource(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		content := make([]byte, 2*readSize+100)
		for i := range content {
			content[i] = byte(i)
		}
		path := filepath.Join(t.TempDir(), "data")
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
		// 8 * readSize bits per second is one read per second
		src, err := NewFileSource(path, media.RateBounds{Initial: 8 * readSize, Max: 8 * readSize}, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		c := &chunkCollector{}
		if err = src.Connect(c); err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		if err = src.Run(context.Background()); err != nil {
			t.Fatal(err)
		}
		// start delay, then reads at 0s, 1s, 2s and the one hitting EOF at 3s
		if elapsed := time.Since(start); elapsed != 4*time.Second {
			t.Errorf("run took %v, want 4s", elapsed)
		}
		if c.eos != 1 {
			t.Fatalf("got %v ends of stream, want 1", c.eos)
		}
		got := bytes.Join(c.chunks, nil)
		want := append(header(uint64(len(content))), content...)
		if !bytes.Equal(got, want) {
			t.Fatalf("got %v bytes, want the header and the %v byte file", len(got), len(content))
		}
		if c.sink.currentChunk != 1 {
			t.Errorf("sink counted %v chunks, want 1", c.sink.currentChunk)
		}
	})
}

func TestFileSourceMissingFile(t *testing.T) {
	if _, err := NewFileSource(filepath.Join(t.TempDir(), "missing"), media.RateBounds{}, 0); err == nil {
		t.Fatal("got no error for a missing file")
	}
}
