package mediafile

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mengelbart/mrtp"
	"github.com/mengelbart/mrtp/pipeline"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIVFSinkRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.ivf")
	file, err := os.Create(path)
	require.NoError(t, err)
	sink := NewIVFSink(file)
	require.NoError(t, sink.Negotiate(mrtp.EncodedVideo{Codec: mrtp.VP8, Width: 64, Height: 48}))

	pool := pipeline.NewPool(func() *mrtp.EncodedFrame { return &mrtp.EncodedFrame{} }, func(*mrtp.EncodedFrame) {})
	payloads := [][]byte{{1, 2, 3}, {4, 5}, {6}}
	for i, payload := range payloads {
		p := pool.Get()
		p.Value().Data = payload
		p.Value().PTS = time.Hour + time.Duration(i)*33*time.Millisecond
		require.NoError(t, sink.Write(p))
	}
	require.NoError(t, sink.Close())

	synctest.Test(t, func(t *testing.T) {
		reader, err := os.Open(path)
		require.NoError(t, err)
		src, err := NewIVFSource(reader)
		require.NoError(t, err)
		defer src.Close()
		assert.Equal(t, mrtp.EncodedVideo{Codec: mrtp.VP8, Width: 64, Height: 48}, src.Format())
		assert.Equal(t, uint32(len(payloads)), src.frames)

		start := time.Now()
		for i, want := range payloads {
			p, err := src.Pull(context.Background())
			require.NoError(t, err)
			pts := time.Duration(i) * 33 * time.Millisecond
			assert.Equal(t, want, p.Value().Data)
			assert.Equal(t, pts, p.Value().PTS)
			assert.Equal(t, pts, time.Since(start))
			p.Release()
		}
		_, err = src.Pull(context.Background())
		assert.ErrorIs(t, err, io.EOF)
	})
}

func TestIVFSinkRejectsH264(t *testing.T) {
	sink := NewIVFSink(nopWriteCloser{})
	assert.Error(t, sink.Negotiate(mrtp.EncodedVideo{Codec: mrtp.H264}))
}

type nopWriteCloser struct{}

func (nopWriteCloser) Write(p []byte) (int, error) { return len(p), nil }
func (nopWriteCloser) Close() error                { return nil }
