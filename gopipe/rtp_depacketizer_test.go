//go:build cgo

package gopipe

import (
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mengelbart/mrtp"
	"github.com/mengelbart/mrtp/pipeline"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// packetizeFrames returns the RTP packets of each frame.
func packetizeFrames(t *testing.T, c mrtp.Codec, frames [][]byte) [][][]byte {
	t.Helper()

	packets := make([][][]byte, 0, len(frames))
	sink := newCollector(mrtp.RTPBytes)

	packetizer := NewRTPPacketizer(1420, 96, 0, 90_000, c)
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

	received := newCollector(encodedBytes)
	depacketizer := NewRTPDepacketizer(depacketizerTimeout, nil)
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
