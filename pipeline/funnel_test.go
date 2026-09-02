package pipeline

import (
	"context"
	"slices"
	"testing"

	"github.com/mengelbart/mrtp"
)

func TestFunnelMergesItsInputs(t *testing.T) {
	pool := newPayloadPool()
	funnel := NewFunnel[payload]()
	sink := &collector{}

	g := NewGraph()
	sources := []*counter{newCounter(pool, 3), newCounter(pool, 3)}
	for _, src := range sources {
		must(t, g.Connect(src, funnel.Input()))
	}
	must(t, g.Connect(funnel, sink))

	if err := g.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := sink.values()
	slices.Sort(got)
	if !slices.Equal(got, []int{0, 0, 1, 1, 2, 2}) {
		t.Fatalf("funnel delivered %v", got)
	}
	if sink.format() != mrtp.Format(rawFormat) {
		t.Fatalf("the funnel's output negotiated %v, want %v", sink.format(), rawFormat)
	}
	// One end of stream, once every input has ended.
	if sink.eos != 1 {
		t.Fatalf("the output saw %v ends of stream, want 1", sink.eos)
	}
	if pool.Outstanding() != 0 {
		t.Fatalf("%v packets were never released", pool.Outstanding())
	}
}

func TestFunnelRefusesDisagreeingInputs(t *testing.T) {
	pool := newPayloadPool()
	funnel := NewFunnel[payload]()
	raw := newCounter(pool, 1)
	encoded := newCounter(pool, 1)
	encoded.format = encodedFormat

	g := NewGraph()
	must(t, g.Connect(raw, funnel.Input()))
	must(t, g.Connect(encoded, funnel.Input()))
	must(t, g.Connect(funnel, &collector{}))

	if err := g.Run(context.Background()); err == nil {
		t.Fatal("the funnel accepted two formats at once")
	}
}
