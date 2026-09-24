//go:build cgo

package gopipe

import (
	"bytes"
	"image"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/mengelbart/mrtp"
	"github.com/mengelbart/mrtp/internal/testvideo"
	"github.com/mengelbart/mrtp/mediafile"
	"github.com/mengelbart/mrtp/pipeline"
	"github.com/mengelbart/y4m"
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

// collector is a Sink that keeps a copy of every payload it is handed, and
// asserts that the packets it was given were its to release.
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

func encodedBytes(f *mrtp.EncodedFrame) *[]byte { return &f.Data }

// encodedFrames returns the synthetic stream encoded with c.
func encodedFrames(t *testing.T, c mrtp.Codec) [][]byte {
	t.Helper()

	reader, _, err := y4m.NewReader(bytes.NewReader(
		testvideo.Y4M(testWidth, testHeight, testFrames, testFPSNum, testFPSDen),
	))
	require.NoError(t, err)
	format := mrtp.RawVideo{
		Width:       testWidth,
		Height:      testHeight,
		Subsampling: image.YCbCrSubsampleRatio420,
		FrameRate:   mrtp.FrameRate{Num: testFPSNum, Den: testFPSDen},
	}
	frames := newCollector(encodedBytes)

	encoder := NewEncoder(c)
	defer func() {
		require.NoError(t, encoder.Close())
	}()

	require.NoError(t, encoder.Negotiate(format))
	require.NoError(t, encoder.Connect(frames))
	require.NoError(t, encoder.SetTargetBitrate(testBitrate))

	pool := rawFramePool(t, format)
	var pts time.Duration
	for {
		packet := pool.Get()
		value := packet.Value()
		if !readFrame(reader, value) {
			packet.Release()
			break
		}
		value.PTS = pts
		value.Duration = testFrameDuration
		pts += testFrameDuration

		require.NoError(t, encoder.Write(packet))
	}
	require.Len(t, frames.items, testFrames)

	return frames.items
}

// rawFramePool is a pool of frames in format f.
func rawFramePool(t *testing.T, f mrtp.RawVideo) *pipeline.Pool[mrtp.RawFrame] {
	t.Helper()

	ySize, cSize, err := f.PlaneSizes()
	require.NoError(t, err)
	return pipeline.NewPool(func() *mrtp.RawFrame {
		buffer := make([]byte, ySize+2*cSize)
		return &mrtp.RawFrame{
			Y:  buffer[:ySize],
			Cb: buffer[ySize : ySize+cSize],
			Cr: buffer[ySize+cSize:],
		}
	}, nil)
}

// readFrame fills frame's planes from reader, reporting whether there was a
// frame left to read.
func readFrame(reader *y4m.Reader, frame *mrtp.RawFrame) bool {
	raw, _, err := reader.ReadNextFrame()
	if err != nil {
		return false
	}
	n := copy(frame.Y, raw)
	n += copy(frame.Cb, raw[n:])
	copy(frame.Cr, raw[n:])
	return true
}
