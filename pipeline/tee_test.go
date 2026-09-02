package pipeline

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/mengelbart/mrtp"
)

func TestTeeClonesToEveryBranch(t *testing.T) {
	pool := newPayloadPool()
	src := newCounter(pool, 3)
	tee := NewTee[payload]()
	branches := []*collector{{}, {}, {}}
	for _, b := range branches {
		if err := tee.AddOutput(b); err != nil {
			t.Fatal(err)
		}
	}

	g := NewGraph()
	must(t, g.Connect(src, tee))
	g.Terminal(src)

	if err := g.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	for i, b := range branches {
		if got := b.values(); !slices.Equal(got, []int{0, 1, 2}) {
			t.Fatalf("branch %v got %v", i, got)
		}
		if b.format() != mrtp.Format(rawFormat) {
			t.Fatalf("branch %v negotiated %v", i, b.format())
		}
		if b.eos != 1 {
			t.Fatalf("branch %v saw %v ends of stream, want 1", i, b.eos)
		}
	}
	if pool.Outstanding() != 0 {
		t.Fatalf("%v packets were never released", pool.Outstanding())
	}
	// The branches are ports the tee owns, so the graph knows and closes them.
	if err := g.Close(); err != nil {
		t.Fatal(err)
	}
	for i, b := range branches {
		if b.closed != 1 {
			t.Fatalf("branch %v was closed %v times, want 1", i, b.closed)
		}
	}
}

func TestTeeNegotiatesABranchAddedLate(t *testing.T) {
	tee := NewTee[payload]()
	if err := tee.Negotiate(rawFormat); err != nil {
		t.Fatal(err)
	}
	late := &collector{}
	if err := tee.AddOutput(late); err != nil {
		t.Fatal(err)
	}
	if late.format() != mrtp.Format(rawFormat) {
		t.Fatalf("the late branch negotiated %v, want %v", late.format(), rawFormat)
	}
}

func TestTeeRefusesWhenABranchDoes(t *testing.T) {
	tee := NewTee[payload]()
	must(t, tee.AddOutput(&collector{}))
	must(t, tee.AddOutput(&collector{reject: errors.New("no")}))
	if err := tee.Negotiate(rawFormat); err == nil {
		t.Fatal("the tee accepted a format one of its branches refused")
	}
}
