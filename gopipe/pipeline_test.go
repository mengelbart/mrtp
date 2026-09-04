//go:build cgo

package gopipe

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/mengelbart/mrtp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// frameCounter counts the frames passing through a pipeline stage.
type frameCounter struct {
	count int
}

func (c *frameCounter) Link(w Sink, _ Info) (Sink, error) {
	return WriterFunc(func(b []byte, a Attributes) error {
		c.count++
		return w.Write(b, a)
	}), nil
}

// TestPipelineEndToEnd checks the wiring of the full send and receive chain. Per codec
// coverage lives in the gopipe/codec tests.
func TestPipelineEndToEnd(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())

		decoder, err := NewDecoder(mrtp.VP8)
		require.NoError(t, err)

		decoded := 0
		decoderSink, err := decoder.Link(WriterFunc(func(_ []byte, a Attributes) error {
			assert.Equal(t, testWidth, a[Width])
			assert.Equal(t, testHeight, a[Height])
			decoded++
			return nil
		}), Info{})
		require.NoError(t, err)

		depacketizer, err := newRTPDepacketizer(depacketizerTimeout, mrtp.VP8, func(frame []byte, pts int64) {
			assert.NoError(t, decoderSink.Write(frame, Attributes{PTS: pts}))
		})
		require.NoError(t, err)

		var wg sync.WaitGroup
		wg.Go(depacketizer.Run)

		src := newTestSource(t)
		i := src.GetInfo()

		encoder := NewEncoder(mrtp.VP8)
		packetizer := &RTPPacketizerFactory{
			MTU:       1420,
			PT:        96,
			SSRC:      0,
			ClockRate: 90_000,
			Codec:     mrtp.VP8,
		}
		pacer := NewFrameSpacer(ctx)
		counter := &frameCounter{}

		sink := WriterFunc(func(b []byte, _ Attributes) error {
			return depacketizer.Write(b)
		})
		chain, err := Chain(i, sink, pacer, packetizer, encoder, counter)
		require.NoError(t, err)
		require.NoError(t, encoder.SetTargetBitrate(testBitrate))

		require.NoError(t, src.StartLive(ctx, chain))
		synctest.Wait()

		assert.Equal(t, testFrames, counter.count)
		assert.Equal(t, counter.count, decoded)

		require.NoError(t, depacketizer.Close())
		require.NoError(t, pacer.Close())
		require.NoError(t, encoder.Close())
		require.NoError(t, decoder.Close())
		cancel()

		wg.Wait()
		synctest.Wait()
	})
}
