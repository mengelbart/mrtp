//go:build cgo

package gopipe

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mengelbart/mrtp"
	"github.com/mengelbart/mrtp/internal/testvideo"
	"github.com/mengelbart/mrtp/mediafile"
	"github.com/mengelbart/mrtp/packetization"
	"github.com/mengelbart/mrtp/pipeline"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testWidth  = 640
	testHeight = 480
	testFrames = 30
	testFPSNum = 30
	testFPSDen = 1
)

const (
	testFrameDuration   = time.Second * testFPSDen / testFPSNum
	depacketizerTimeout = 10 * time.Millisecond
)

func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(slog.DiscardHandler))
	os.Exit(m.Run())
}

// newTestSource returns a Y4MSource over the synthetic stream.
func newTestSource(t *testing.T) *mediafile.Y4MSource {
	t.Helper()

	src, err := mediafile.NewY4MSource(bytes.NewReader(
		testvideo.Y4M(testWidth, testHeight, testFrames, testFPSNum, testFPSDen),
	))
	require.NoError(t, err)
	return src
}

// collector is a Sink that keeps a copy of every payload it is handed.
type collector[T any] struct {
	bytes func(*T) *[]byte
	items [][]byte
	eos   bool
}

func newCollector[T any](bytes func(*T) *[]byte) *collector[T] {
	return &collector[T]{bytes: bytes}
}

func (c *collector[T]) Negotiate(mrtp.Format) error { return nil }

func (c *collector[T]) Write(p mrtp.Packet[T]) error {
	defer p.Release()
	c.items = append(c.items, bytes.Clone(*c.bytes(p.Value())))
	return nil
}

func (c *collector[T]) EndOfStream() error {
	c.eos = true
	return nil
}

func (c *collector[T]) Close() error { return nil }

// counter counts the packets passing through a point in a graph.
type counter[T any] struct {
	count  int
	down   mrtp.Sink[T]
	format mrtp.Format
}

func (c *counter[T]) Negotiate(f mrtp.Format) error {
	c.format = f
	return nil
}

func (c *counter[T]) Format() mrtp.Format { return c.format }

func (c *counter[T]) Connect(down mrtp.Sink[T]) error {
	c.down = down
	return nil
}

func (c *counter[T]) Write(p mrtp.Packet[T]) error {
	c.count++
	return c.down.Write(p)
}

func (c *counter[T]) EndOfStream() error { return c.down.EndOfStream() }

func (c *counter[T]) Close() error { return nil }

// TestPipelineEndToEnd checks the wiring of the full send and receive chain. Per codec
// coverage lives in the codec tests.
func TestPipelineEndToEnd(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		src := newTestSource(t)
		encoder := NewEncoder(mrtp.VP8)
		frames := &counter[mrtp.EncodedFrame]{}
		packetizer := packetization.NewRTPPacketizer(1420, 96, 0, 90_000, mrtp.VP8)
		queue := pipeline.NewQueue(1000, pipeline.PaceFrames(
			(*mrtp.RTPPacket).Marker, testFrameDuration,
		))
		pump := pipeline.NewPump[mrtp.RTPPacket]()

		depacketizer := packetization.NewRTPDepacketizer(depacketizerTimeout, nil)
		decoder, err := NewDecoder(mrtp.VP8)
		require.NoError(t, err)
		decoded := newCollector(func(f *mrtp.RawFrame) *[]byte { return &f.Y })

		g := pipeline.NewGraph()
		require.NoError(t, g.Connect(src, encoder))
		require.NoError(t, g.Connect(encoder, frames))
		require.NoError(t, g.Connect(frames, packetizer))
		require.NoError(t, g.Connect(packetizer, queue))
		require.NoError(t, g.Attach(queue, pump))
		require.NoError(t, g.Connect(pump, depacketizer))
		require.NoError(t, g.Connect(depacketizer, decoder))
		require.NoError(t, g.Connect(decoder, decoded))
		// no terminal driver: Run returns once end of stream has reached the
		// far end, rather than when the source stops producing

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		require.NoError(t, g.Run(ctx))
		synctest.Wait()

		assert.Equal(t, testFrames, frames.count)
		assert.Equal(t, frames.count, len(decoded.items))
		assert.True(t, decoded.eos)

		format, ok := decoder.Format().(mrtp.RawVideo)
		require.True(t, ok)
		assert.Equal(t, uint(testWidth), format.Width)
		assert.Equal(t, uint(testHeight), format.Height)

		require.NoError(t, g.Close())

		// every packet the graph handed on was released exactly once
		for name, outstanding := range map[string]int{
			"encoder": encoder.pool.Outstanding(),
			"decoder": decoder.pool.Outstanding(),
		} {
			assert.Zero(t, outstanding, "%v packets from the %v were never released", outstanding, name)
		}
	})
}
