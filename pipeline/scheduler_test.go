package pipeline

import (
	"context"
	"slices"
	"testing"
	"time"
)

func TestSchedulerHonoursItsSelector(t *testing.T) {
	pool := newPayloadPool()
	high := newList(pool, 0, 1)
	low := newList(pool, 10, 11)
	sched := NewScheduler[payload](Priority[payload]())
	sched.AddInput(high)
	sched.AddInput(low)
	t.Cleanup(func() { must(t, sched.Close()) })

	ctx := context.Background()
	sched.lock.Lock()
	sched.startLocked(ctx)
	sched.lock.Unlock()
	waitForStaged(t, sched, 2)

	// With a packet ready on both inputs, priority takes the first input.
	p, err := sched.Pull(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if p.Value().n != 0 {
		t.Fatalf("priority took %v, want the first input's packet", p.Value().n)
	}
	p.Release()
}

func TestSchedulerDeliversEveryInputAndEnds(t *testing.T) {
	pool := newPayloadPool()
	sched := NewScheduler[payload](Priority[payload]())
	sched.AddInput(newList(pool, 0, 1, 2))
	sched.AddInput(newList(pool, 10, 11))
	out := &drain{}

	g := NewGraph()
	must(t, g.Attach(sched, out))

	if err := g.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := out.values()
	slices.Sort(got)
	if !slices.Equal(got, []int{0, 1, 2, 10, 11}) {
		t.Fatalf("the scheduler delivered %v", got)
	}
	if err := g.Close(); err != nil {
		t.Fatal(err)
	}
	if pool.Outstanding() != 0 {
		t.Fatalf("%v packets were never released", pool.Outstanding())
	}
}

func TestSchedulerRefusesDisagreeingInputs(t *testing.T) {
	pool := newPayloadPool()
	first := newList(pool, 0)
	second := newList(pool, 1)
	second.format = rawFormat
	sched := NewScheduler[payload](Priority[payload]())
	sched.AddInput(first)
	sched.AddInput(second)

	sched.Format()
	if _, err := sched.Pull(context.Background()); err == nil {
		t.Fatal("the scheduler pulled from inputs carrying two formats")
	}
}

func TestPrioritySelectsTheFirstReadyInput(t *testing.T) {
	s := Priority[payload]()
	if got := s.Select([]*payload{nil, {}, {}}); got != 1 {
		t.Fatalf("priority chose %v, want 1", got)
	}
}

func TestWeightsSharesInProportion(t *testing.T) {
	s := Weights[payload](3, 1)
	ready := []*payload{{}, {}}
	taken := []int{0, 0}
	for range 8 {
		taken[s.Select(ready)]++
	}
	if taken[0] != 6 || taken[1] != 2 {
		t.Fatalf("weights 3:1 gave %v, want 6 and 2", taken)
	}
}

func TestWeightsSkipsAnInputWithNothingReady(t *testing.T) {
	s := Weights[payload](1, 1)
	if got := s.Select([]*payload{nil, {}}); got != 1 {
		t.Fatalf("weights chose %v, want the only ready input", got)
	}
}

// waitForStaged waits until n of the scheduler's inputs hold a packet.
func waitForStaged(t *testing.T, s *Scheduler[payload], n int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		staged := 0
		for _, in := range s.inputs {
			if p, _ := in.staged(); p != nil {
				staged++
			}
		}
		if staged >= n {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("only some of %v inputs staged a packet", n)
}
