//go:build cgo

package gopipe

import (
	"bytes"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mengelbart/mrtp"
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

			want := make([][]byte, 0, len(frames)-len(droppedFrames))
			for i, frame := range frames {
				if slices.Contains(droppedFrames, i) {
					framePackets[i] = nil
					continue
				}
				want = append(want, frame)
			}

			synctest.Test(t, func(t *testing.T) {
				received := runDepacketizer(t, tc.codec, framePackets)

				assert.Len(t, received, len(want))
				if tc.exactPayload {
					assert.Equal(t, want, received)
				}
			})
		})
	}
}

func packetizeFrames(t *testing.T, c mrtp.Codec, frames [][]byte) [][][]byte {
	t.Helper()

	packets := make([][][]byte, 0, len(frames))
	sink := WriterFunc(func(b []byte, _ Attributes) error {
		packets[len(packets)-1] = append(packets[len(packets)-1], bytes.Clone(b))
		return nil
	})

	factory := &RTPPacketizerFactory{
		MTU:       1420,
		PT:        96,
		SSRC:      0,
		ClockRate: 90_000,
		Codec:     c,
	}
	packetizer, err := factory.Link(sink, Info{TimebaseNum: testFPSNum, TimebaseDen: testFPSDen})
	require.NoError(t, err)

	var pts int64
	for _, frame := range frames {
		packets = append(packets, nil)
		require.NoError(t, packetizer.Write(frame, Attributes{PTS: pts}))
		pts += testFrameDuration.Microseconds()
	}
	return packets
}

// runDepacketizer feeds one frame of RTP packets per frame duration and returns the assembled
// frames. It must run inside a synctest bubble so the depacketizer timeouts cost no real time.
func runDepacketizer(t *testing.T, c mrtp.Codec, framePackets [][][]byte) [][]byte {
	t.Helper()

	received := make([][]byte, 0, len(framePackets))
	depacketizer, err := newRTPDepacketizer(depacketizerTimeout, c, func(frame []byte, _ int64) {
		received = append(received, bytes.Clone(frame))
	})
	require.NoError(t, err)

	var wg sync.WaitGroup
	wg.Go(depacketizer.Run)

	for _, packets := range framePackets {
		for _, packet := range packets {
			require.NoError(t, depacketizer.Write(packet))
		}
		time.Sleep(testFrameDuration)
	}
	synctest.Wait()

	require.NoError(t, depacketizer.Close())
	wg.Wait()

	return received
}
