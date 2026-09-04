package pipeline

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
)

// terminalGraph is one counter feeding one collector, ended by the counter.
func terminalGraph(t *testing.T, n int) (*Graph, *collector) {
	t.Helper()
	pool := newPayloadPool()
	src := newCounter(pool, n)
	sink := &collector{}
	g := NewGraph()
	must(t, g.Connect(src, sink))
	g.Terminal(src)
	return g, sink
}

func TestRunnerEndsWithATerminalGraph(t *testing.T) {
	g, sink := terminalGraph(t, 4)
	blocking := NewGraph()
	blocking.Add(blocker{})

	r := NewRunner()
	r.Add(blocking)
	r.Add(g)

	if err := r.Run(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := sink.values(); !slices.Equal(got, []int{0, 1, 2, 3}) {
		t.Fatalf("sink got %v", got)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if sink.closed != 1 {
		t.Fatalf("sink was closed %v times, want 1", sink.closed)
	}
}

func TestRunnerRunsUntilCancelledWithoutATerminalGraph(t *testing.T) {
	g := NewGraph()
	g.Add(blocker{})

	r := NewRunner()
	r.Add(g)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()

	time.Sleep(10 * time.Millisecond)
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestRunnerReportsAFailingGraph(t *testing.T) {
	fail := errors.New("nope")
	g := NewGraph()
	g.Add(failingDriver{err: fail})
	blocking := NewGraph()
	blocking.Add(blocker{})

	r := NewRunner()
	r.Add(g)
	r.Add(blocking)

	if err := r.Run(t.Context()); !errors.Is(err, fail) {
		t.Fatalf("run returned %v, want %v", err, fail)
	}
}

func TestRunnerStartsAGraphAddedWhileRunning(t *testing.T) {
	blocking := NewGraph()
	blocking.Add(blocker{})
	r := NewRunner()
	r.Add(blocking)

	late, sink := terminalGraph(t, 2)
	done := make(chan error, 1)
	go func() { done <- r.Run(t.Context()) }()

	// The runner is either running or about to, and Add covers both.
	r.Add(late)

	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := sink.values(); !slices.Equal(got, []int{0, 1}) {
		t.Fatalf("sink got %v", got)
	}
}

// failingDriver is a driver that fails as soon as it runs.
type failingDriver struct{ err error }

func (d failingDriver) Run(context.Context) error { return d.err }

func (d failingDriver) Close() error { return nil }
