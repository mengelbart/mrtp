package mediafile

import (
	"bytes"
	"context"
	"testing"
	"testing/synctest"

	"github.com/mengelbart/mrtp"
	"github.com/mengelbart/mrtp/internal/testvideo"
	"github.com/mengelbart/mrtp/pipeline"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestY4MSourceReadsAndReleases(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		src, err := NewY4MSource(bytes.NewReader(testvideo.Y4M(64, 48, 5, 30, 1)))
		require.NoError(t, err)
		discard := pipeline.NewDiscard[mrtp.RawFrame]()

		g := pipeline.NewGraph()
		require.NoError(t, g.Connect(src, discard))
		g.Terminal(src)
		require.NoError(t, g.Run(context.Background()))
		require.NoError(t, g.Close())

		assert.Equal(t, uint64(5), discard.Packets())
		assert.Zero(t, src.pool.Outstanding())
	})
}
