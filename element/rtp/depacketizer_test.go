//go:build cgo

package rtp

import (
	"bytes"
	"log/slog"
	"os"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mengelbart/mrtp"
	"github.com/mengelbart/mrtp/codec"
	"github.com/mengelbart/mrtp/codec/vpx"
	"github.com/mengelbart/mrtp/codec/x264"
	"github.com/mengelbart/mrtp/internal/testvideo"
	"github.com/mengelbart/mrtp/pipeline"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The resolution and bitrate are chosen so encoded frames span several RTP packets, which
// exercises reassembly and gets the depacketizer past its lateness threshold.
const (
	testWidth   = 640
	testHeight  = 480
	testFrames  = 30
	testFPSNum  = 30
	testFPSDen  = 1
	testBitrate = 4_000_000
)

const (
	testFrameDuration   = time.Second * testFPSDen / testFPSNum
	depacketizerTimeout = 10 * time.Millisecond
)

var depacketizerCodecs = []struct {
	codec mrtp.Codec
	// H264 payloads are rewritten with Annex-B start codes, so only the frame count is comparable
	exactPayload bool
}{
	{mrtp.VP8, true},
	{mrtp.VP9, true},
	{mrtp.H264, false},
}

func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(slog.DiscardHandler))
	os.Exit(m.Run())
}

func TestDepacketizerRoundtrip(t *testing.T) {
	for _, tc := range depacketizerCodecs {
		t.Run(tc.codec.String(), func(t *testing.T) {
			frames := encodedFrames(t, tc.codec)
			framePackets := packetizeFrames(t, tc.codec, frames)

			synctest.Test(t, func(t *testing.T) {
				received := runDepacketizer(t, tc.codec, framePackets)

				assert.Len(t, received, len(frames))
				if tc.exactPayload {
					assert.Equal(t, frames, received)
				}
			})
		})
	}
}

func TestDepacketizerRTPDrops(t *testing.T) {
	droppedFrames := []int{3, 24, 25}

	for _, tc := range depacketizerCodecs {
		t.Run(tc.codec.String(), func(t *testing.T) {
			frames := encodedFrames(t, tc.codec)
			framePackets := packetizeFrames(t, tc.codec, frames)

			kept := make([][]byte, 0, len(frames)-len(droppedFrames))
			for i, frame := range frames {
				if slices.Contains(droppedFrames, i) {
					framePackets[i] = nil
					continue
				}
				kept = append(kept, frame)
			}

			synctest.Test(t, func(t *testing.T) {
				received := runDepacketizer(t, tc.codec, framePackets)

				assert.Len(t, received, len(kept))
				if tc.exactPayload {
					assert.Equal(t, kept, received)
				}
			})
		})
	}
}

// collector is a Sink that keeps a copy of every payload it is handed.
type collector[T any] struct {
	bytes func(*T) *[]byte
	items [][]byte
}

func (c *collector[T]) Negotiate(mrtp.Format) error { return nil }

func (c *collector[T]) Write(p mrtp.Packet[T]) error {
	defer p.Release()
	c.items = append(c.items, bytes.Clone(*c.bytes(p.Value())))
	return nil
}

func (c *collector[T]) EndOfStream() error { return nil }

func (c *collector[T]) Close() error { return nil }

// encodedFrames returns the synthetic stream encoded with c.
func encodedFrames(t *testing.T, c mrtp.Codec) [][]byte {
	t.Helper()

	config := codec.Config{
		Codec:      c,
		Width:      testWidth,
		Height:     testHeight,
		FrameRate:  mrtp.FrameRate{Num: testFPSNum, Den: testFPSDen},
		TargetRate: testBitrate,
	}
	var (
		enc codec.Encoder
		err error
	)
	switch c {
	case mrtp.VP8, mrtp.VP9:
		enc, err = vpx.NewEncoder(config)
	case mrtp.H264:
		enc, err = x264.NewEncoder(config)
	default:
		t.Fatalf("unsupported codec: %v", c)
	}
	require.NoError(t, err)
	defer func() {
		require.NoError(t, enc.Close())
	}()

	frames := make([][]byte, 0, testFrames)
	for i := range testFrames {
		frame, err := enc.Encode(
			testvideo.Image(testWidth, testHeight, i),
			int64(i)*testFrameDuration.Microseconds(),
			testFrameDuration,
		)
		require.NoError(t, err)
		frames = append(frames, bytes.Clone(frame.Payload))
	}
	return frames
}

// packetizeFrames returns the RTP packets of each frame.
func packetizeFrames(t *testing.T, c mrtp.Codec, frames [][]byte) [][][]byte {
	t.Helper()

	packets := make([][][]byte, 0, len(frames))
	sink := &collector[mrtp.RTPPacket]{bytes: mrtp.RTPBytes}

	packetizer := NewPacketizer(1420, 96, 0, 90_000, c)
	require.NoError(t, packetizer.Negotiate(mrtp.EncodedVideo{Codec: c}))
	require.NoError(t, packetizer.Connect(sink))

	pool := pipeline.NewPool(
		func() *mrtp.EncodedFrame { return &mrtp.EncodedFrame{} },
		func(f *mrtp.EncodedFrame) { f.Data = f.Data[:0] },
	)
	var pts time.Duration
	for _, frame := range frames {
		sink.items = nil
		packet := pool.Get()
		value := packet.Value()
		value.Data = append(value.Data[:0], frame...)
		value.PTS = pts
		value.Duration = testFrameDuration
		pts += testFrameDuration

		require.NoError(t, packetizer.Write(packet))
		packets = append(packets, sink.items)
	}
	return packets
}

// runDepacketizer feeds one frame of RTP packets per frame duration and returns the
// assembled frames. It must run inside a synctest bubble so the depacketizer timeouts cost
// no real time.
func runDepacketizer(t *testing.T, c mrtp.Codec, framePackets [][][]byte) [][]byte {
	t.Helper()

	received := &collector[mrtp.EncodedFrame]{
		bytes: func(f *mrtp.EncodedFrame) *[]byte { return &f.Data },
	}
	depacketizer := NewDepacketizer(depacketizerTimeout, nil)
	require.NoError(t, depacketizer.Negotiate(mrtp.RTP{
		Codec:       c,
		PayloadType: 96,
		ClockRate:   90_000,
	}))
	require.NoError(t, depacketizer.Connect(received))

	pool := pipeline.NewPool(
		func() *mrtp.RTPPacket { return &mrtp.RTPPacket{} },
		func(p *mrtp.RTPPacket) { p.Data = p.Data[:0] },
	)
	for _, packets := range framePackets {
		for _, packet := range packets {
			p := pool.Get()
			value := p.Value()
			value.Data = append(value.Data[:0], packet...)
			require.NoError(t, depacketizer.Write(p))
		}
		time.Sleep(testFrameDuration)
	}
	synctest.Wait()
	require.NoError(t, depacketizer.Close())

	return received.items
}
