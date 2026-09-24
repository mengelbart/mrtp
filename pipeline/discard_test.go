package pipeline

import (
	"context"
	"testing"

	"github.com/mengelbart/mrtp"
)

func TestDiscardCountsAndReleases(t *testing.T) {
	r := &packetReader{packets: [][]byte{{1}, {2}, {3}}}
	src := SourceFromReader(r, mrtp.Data{}, 16, chunkBytes)
	discard := NewDiscard[mrtp.DataChunk]()

	g := NewGraph()
	must(t, g.Connect(src, discard))
	g.Terminal(src.(mrtp.Driver))
	must(t, g.Run(context.Background()))
	must(t, g.Close())

	if got := discard.Packets(); got != 3 {
		t.Fatalf("discarded %v packets, want 3", got)
	}
	if n := src.(*readerSource[mrtp.DataChunk]).pool.Outstanding(); n != 0 {
		t.Fatalf("%v packets were never released", n)
	}
}
