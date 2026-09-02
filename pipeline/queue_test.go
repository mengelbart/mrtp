package pipeline

import (
	"context"
	"errors"
	"io"
	"slices"
	"testing"
	"time"

	"github.com/mengelbart/mrtp"
)

// accessUnits ends an access unit at every marked packet and paces nothing, so
// a test sees the queue's own behaviour.
type accessUnits struct{}

func (accessUnits) Boundary(p *payload) bool          { return p.marker }
func (accessUnits) Delay(*payload, int) time.Duration { return 0 }

func TestQueueBridgesPushToPull(t *testing.T) {
	pool := newPayloadPool()
	src := newCounter(pool, 5)
	q := NewQueue[payload](8, accessUnits{})
	out := &drain{}

	g := NewGraph()
	must(t, g.Connect(src, q))
	must(t, g.Attach(q, out))

	if err := g.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := out.values(); !slices.Equal(got, []int{0, 1, 2, 3, 4}) {
		t.Fatalf("drain got %v", got)
	}
	if out.format != mrtp.Format(rawFormat) {
		t.Fatalf("drain attached to format %v, want %v", out.format, rawFormat)
	}
	if pool.Outstanding() != 0 {
		t.Fatalf("%v packets were never released", pool.Outstanding())
	}
}

func TestQueueDropsWholeAccessUnits(t *testing.T) {
	pool := newPayloadPool()
	q := NewQueue[payload](4, accessUnits{})
	for i := range 6 {
		p := pool.Get()
		p.Value().n = i
		p.Value().marker = i%2 == 1
		if err := q.Write(p); err != nil {
			t.Fatal(err)
		}
	}
	if err := q.EndOfStream(); err != nil {
		t.Fatal(err)
	}

	var got []int
	for {
		p, err := q.Pull(context.Background())
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, p.Value().n)
		p.Release()
	}
	// The full queue held the access units 0,1 and 2,3. It dropped the first
	// of them whole rather than blocking its producer.
	if !slices.Equal(got, []int{2, 3, 4, 5}) {
		t.Fatalf("queue delivered %v, want the two newest access units", got)
	}
	if pool.Outstanding() != 0 {
		t.Fatalf("%v dropped packets were never released", pool.Outstanding())
	}
}

func TestQueuePacesWhatIsQueued(t *testing.T) {
	pool := newPayloadPool()
	q := NewQueue[payload](8, PaceFrames(boundary, 100*time.Millisecond))
	for i := range 3 {
		p := pool.Get()
		p.Value().n = i
		if err := q.Write(p); err != nil {
			t.Fatal(err)
		}
	}
	start := time.Now()
	for range 3 {
		p, err := q.Pull(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		p.Release()
	}
	// 0.3 of a frame spread over the two packets behind the first one.
	if elapsed := time.Since(start); elapsed < 20*time.Millisecond {
		t.Fatalf("three packets left in %v, too fast to have been paced", elapsed)
	}
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestPaceFramesSpreadsTheBacklog(t *testing.T) {
	p := PaceFrames(boundary, 100*time.Millisecond)
	if d := p.Delay(nil, 0); d != 0 {
		t.Fatalf("delay with an empty queue is %v, want 0", d)
	}
	if d := p.Delay(nil, 1); d != 30*time.Millisecond {
		t.Fatalf("delay behind one packet is %v, want 30ms", d)
	}
	if d := p.Delay(nil, 3); d != 10*time.Millisecond {
		t.Fatalf("delay behind three packets is %v, want 10ms", d)
	}
}

func TestQueueCarriesADownstreamFailureBack(t *testing.T) {
	pool := newPayloadPool()
	failure := errors.New("the transport is gone")
	q := NewQueue[payload](4, nil)
	q.Fail(failure)

	if err := q.Write(pool.Get()); !errors.Is(err, failure) {
		t.Fatalf("Write returned %v, want %v", err, failure)
	}
	if pool.Outstanding() != 0 {
		t.Fatal("the rejected packet was not released")
	}
}

func TestQueueReleasesWhatItHoldsOnClose(t *testing.T) {
	pool := newPayloadPool()
	q := NewQueue[payload](4, nil)
	for range 3 {
		if err := q.Write(pool.Get()); err != nil {
			t.Fatal(err)
		}
	}
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
	if pool.Outstanding() != 0 {
		t.Fatalf("%v packets survived Close", pool.Outstanding())
	}
}

func TestQueuePullWaitsForAPacket(t *testing.T) {
	pool := newPayloadPool()
	q := NewQueue[payload](4, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := q.Pull(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Pull on an empty queue returned %v", err)
	}
	go func() {
		time.Sleep(5 * time.Millisecond)
		if err := q.Write(pool.Get()); err != nil {
			t.Error(err)
		}
	}()
	p, err := q.Pull(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	p.Release()
}
