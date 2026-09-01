//go:build cgo

package gopipe

import (
	"bytes"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/mengelbart/mrtp"
	"github.com/mengelbart/mrtp/internal/testvideo"
	"github.com/stretchr/testify/require"
)

// The resolution and bitrate are chosen so encoded frames span several RTP packets, which
// exercises reassembly and gets the jitter buffer past its 50 packet emit threshold.
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

func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(slog.DiscardHandler))
	os.Exit(m.Run())
}

// newTestSource returns a Y4MSource over the synthetic stream.
func newTestSource(t *testing.T) *Y4MSource {
	t.Helper()

	src, err := NewY4MSource(bytes.NewReader(
		testvideo.Y4M(testWidth, testHeight, testFrames, testFPSNum, testFPSDen),
	))
	require.NoError(t, err)
	return src
}

// encodedFrames returns the synthetic stream encoded with c.
func encodedFrames(t *testing.T, c mrtp.Codec) [][]byte {
	t.Helper()

	src := newTestSource(t)

	frames := make([][]byte, 0, testFrames)
	sink := WriterFunc(func(b []byte, _ Attributes) error {
		frames = append(frames, bytes.Clone(b))
		return nil
	})

	encoder := NewEncoder(c)
	defer func() {
		require.NoError(t, encoder.Close())
	}()

	chain, err := Chain(src.GetInfo(), sink, encoder)
	require.NoError(t, err)
	require.NoError(t, encoder.SetTargetBitrate(testBitrate))

	var pts int64
	for {
		frame, attr, err := src.getFrame()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)

		attr[PTS] = pts
		attr[FrameDuration] = testFrameDuration
		pts += testFrameDuration.Microseconds()

		require.NoError(t, chain.Write(frame, attr))
	}
	require.Len(t, frames, testFrames)

	return frames
}
