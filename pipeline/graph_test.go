package pipeline

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/mengelbart/mrtp"
)

func TestGraphRunsAPushSegment(t *testing.T) {
	pool := newPayloadPool()
	src := newCounter(pool, 4)
	enc := &doubler{pool: pool}
	sink := &collector{}

	g := NewGraph()
	// Wired downstream first, to show that Run orders the negotiation rather
	// than the wiring order doing it.
	if err := g.Connect(enc, sink); err != nil {
		t.Fatal(err)
	}
	if err := g.Connect(src, enc); err != nil {
		t.Fatal(err)
	}
	g.Terminal(src)

	if err := g.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := sink.values(); !slices.Equal(got, []int{0, 2, 4, 6}) {
		t.Fatalf("sink got %v", got)
	}
	if got := sink.format(); got != mrtp.Format(encodedFormat) {
		t.Fatalf("sink negotiated %v, want %v", got, encodedFormat)
	}
	if sink.eos != 1 {
		t.Fatalf("sink saw %v ends of stream, want 1", sink.eos)
	}
	if pool.Outstanding() != 0 {
		t.Fatalf("%v packets were never released", pool.Outstanding())
	}
	if err := g.Close(); err != nil {
		t.Fatal(err)
	}
	if sink.closed != 1 {
		t.Fatalf("sink was closed %v times, want 1", sink.closed)
	}
}

func TestGraphReportsNegotiationFailure(t *testing.T) {
	pool := newPayloadPool()
	src := newCounter(pool, 4)
	sink := &collector{reject: errors.New("no")}

	g := NewGraph()
	if err := g.Connect(src, sink); err != nil {
		t.Fatal(err)
	}
	g.Terminal(src)

	err := g.Run(context.Background())
	if err == nil || !errors.Is(err, sink.reject) {
		t.Fatalf("Run returned %v, want the sink's refusal", err)
	}
	if len(sink.values()) != 0 {
		t.Fatal("a driver ran although negotiation failed")
	}
}

func TestGraphNegotiationTypeMismatch(t *testing.T) {
	pool := newPayloadPool()
	src := newCounter(pool, 1)
	src.format = encodedFormat
	enc := &doubler{pool: pool}
	sink := &collector{}

	g := NewGraph()
	must(t, g.Connect(src, enc))
	must(t, g.Connect(enc, sink))
	g.Terminal(src)

	if err := g.Run(context.Background()); err == nil {
		t.Fatal("a chain that cannot carry its format was built anyway")
	}
}

func TestGraphTerminalDriverEndsTheGraph(t *testing.T) {
	pool := newPayloadPool()
	src := newCounter(pool, 2)
	sink := &collector{}

	g := NewGraph()
	must(t, g.Connect(src, sink))
	g.Add(blocker{})
	g.Terminal(src)

	done := make(chan error, 1)
	go func() { done <- g.Run(context.Background()) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not return when its terminal driver finished")
	}
}

func TestGraphWithoutTerminalWaitsForEveryDriver(t *testing.T) {
	pool := newPayloadPool()
	src := newCounter(pool, 2)
	sink := &collector{}

	g := NewGraph()
	must(t, g.Connect(src, sink))
	g.Add(blocker{})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- g.Run(ctx) }()
	select {
	case <-done:
		t.Fatal("Run returned while a driver was still running")
	case <-time.After(20 * time.Millisecond):
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run returned %v, want the cancellation", err)
	}
}

func TestGraphFirstErrorStopsTheRest(t *testing.T) {
	pool := newPayloadPool()
	failure := errors.New("downstream is gone")
	src := newCounter(pool, 2)
	sink := &collector{writeErr: failure}

	g := NewGraph()
	must(t, g.Connect(src, sink))
	g.Add(blocker{})

	if err := g.Run(context.Background()); !errors.Is(err, failure) {
		t.Fatalf("Run returned %v, want %v", err, failure)
	}
}

func TestGraphRejectsACycle(t *testing.T) {
	pool := newPayloadPool()
	first := &doubler{pool: pool}
	second := &doubler{pool: pool}

	g := NewGraph()
	must(t, g.Connect(first, second))
	must(t, g.Connect(second, first))

	if err := g.Run(context.Background()); err == nil {
		t.Fatal("a cycle was accepted")
	}
}

func TestGraphNeedsADriver(t *testing.T) {
	pool := newPayloadPool()
	g := NewGraph()
	must(t, g.Connect(&doubler{pool: pool}, &collector{}))
	if err := g.Run(context.Background()); err == nil {
		t.Fatal("a graph with nothing to drive it ran")
	}
}
