package pipeline

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/mengelbart/mrtp"
)

func TestPumpDrivesPullIntoPush(t *testing.T) {
	pool := newPayloadPool()
	src := newList(pool, 1, 2, 3)
	pump := NewPump[payload]()
	sink := &collector{}

	g := NewGraph()
	must(t, g.Attach(src, pump))
	must(t, g.Connect(pump, sink))

	if err := g.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := sink.values(); !slices.Equal(got, []int{1, 2, 3}) {
		t.Fatalf("the pump delivered %v", got)
	}
	if sink.format() != mrtp.Format(encodedFormat) {
		t.Fatalf("the pump's output negotiated %v, want %v", sink.format(), encodedFormat)
	}
	if sink.eos != 1 {
		t.Fatalf("the output saw %v ends of stream, want 1", sink.eos)
	}
	if pool.Outstanding() != 0 {
		t.Fatalf("%v packets were never released", pool.Outstanding())
	}
}

func TestPumpReportsADownstreamFailureUpstream(t *testing.T) {
	pool := newPayloadPool()
	failure := errors.New("the transport is gone")
	q := NewQueue[payload](4, nil)
	must(t, q.Negotiate(rawFormat))
	pump := NewPump[payload]()
	sink := &collector{writeErr: failure}

	g := NewGraph()
	must(t, g.Attach(q, pump))
	must(t, g.Connect(pump, sink))

	if err := q.Write(pool.Get()); err != nil {
		t.Fatal(err)
	}
	if err := g.Run(context.Background()); !errors.Is(err, failure) {
		t.Fatalf("Run returned %v, want %v", err, failure)
	}
	// The queue is the boundary between two segments, so it is what carries
	// the failure back to the producer.
	if err := q.Write(pool.Get()); !errors.Is(err, failure) {
		t.Fatalf("the queue's next Write returned %v, want %v", err, failure)
	}
	if pool.Outstanding() != 0 {
		t.Fatalf("%v packets were never released", pool.Outstanding())
	}
}
