package synthetic

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mengelbart/mrtp"
)

// record is what a test keeps of a Write, with the time it was emitted.
type record struct {
	size     int
	zeros    bool
	pts      time.Duration
	duration time.Duration
	burst    int
	first    bool
	last     bool
	at       time.Duration
}

func collect(t *testing.T, g *Generator, ctx context.Context) ([]record, error) {
	t.Helper()
	start := time.Now()
	var records []record
	err := g.Run(ctx, func(w Write) error {
		records = append(records, record{
			size:     len(w.Payload),
			zeros:    bytes.Count(w.Payload, []byte{0}) == len(w.Payload),
			pts:      w.PTS,
			duration: w.Duration,
			burst:    w.Burst,
			first:    w.First,
			last:     w.Last,
			at:       time.Since(start),
		})
		return nil
	})
	return records, err
}

func mustNew(t *testing.T, config Config) *Generator {
	t.Helper()
	g, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func TestIntervalPacing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// 7 fps for one second at 56 kbit/s is 7 writes of 1000 bytes
		g := mustNew(t, Config{
			Pacing:    Interval,
			FrameRate: mrtp.FrameRate{Num: 7, Den: 1},
			Bounds:    mrtp.RateBounds{Initial: 56_000, Min: 1, Max: 1_000_000},
			Duration:  time.Second,
		})
		records, err := collect(t, g, context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if len(records) != 7 {
			t.Fatalf("got %v writes, want 7", len(records))
		}
		var end time.Duration
		for i, r := range records {
			if r.size != 1000 || !r.zeros {
				t.Errorf("write %v has %v bytes, zeros=%v, want 1000 zeros", i, r.size, r.zeros)
			}
			if r.pts != end {
				t.Errorf("write %v starts at %v, want the previous write's end %v", i, r.pts, end)
			}
			if r.at != r.pts {
				t.Errorf("write %v emitted at %v, want its pts %v", i, r.at, r.pts)
			}
			if r.first != (i == 0) || r.burst != -1 {
				t.Errorf("write %v has first=%v burst=%v", i, r.first, r.burst)
			}
			end = r.pts + r.duration
		}
		if end != time.Second {
			t.Errorf("writes end at %v, want 1s", end)
		}
	})
}

func TestIntervalCatchesUp(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		g := mustNew(t, Config{
			Pacing:    Interval,
			FrameRate: mrtp.FrameRate{Num: 10, Den: 1},
			Bounds:    mrtp.RateBounds{Initial: 8000, Max: 8000},
			Duration:  time.Second,
		})
		start := time.Now()
		var at []time.Duration
		err := g.Run(context.Background(), func(w Write) error {
			at = append(at, time.Since(start))
			if len(at) == 1 {
				// the first write is 250 ms late for the next ones
				time.Sleep(250 * time.Millisecond)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		want := []time.Duration{0, 250, 250, 300, 400, 500, 600, 700, 800, 900}
		for i := range want {
			want[i] *= time.Millisecond
		}
		if len(at) != len(want) {
			t.Fatalf("got %v writes, want %v", len(at), len(want))
		}
		for i := range want {
			if at[i] != want[i] {
				t.Errorf("write %v at %v, want %v", i, at[i], want[i])
			}
		}
	})
}

func TestSetTargetBitrateClamps(t *testing.T) {
	g := mustNew(t, Config{
		Pacing:    Interval,
		FrameRate: mrtp.FrameRate{Num: 30, Den: 1},
		Bounds:    mrtp.RateBounds{Initial: 5, Min: 100, Max: 200},
	})
	if got := g.bitrate.Load(); got != 100 {
		t.Fatalf("initial bitrate is %v, want it clamped to 100", got)
	}
	if got := g.SetTargetBitrate(1000); got != 200 {
		t.Fatalf("bitrate is %v, want it clamped to 200", got)
	}

	s := mustNew(t, Config{Pacing: Size, Size: 10, Bounds: mrtp.RateBounds{Min: 100, Max: 200}})
	if got := s.SetTargetBitrate(50); got != 100 {
		t.Fatalf("bitrate is %v, want it clamped to 100", got)
	}
	u := mustNew(t, Config{Pacing: Size, Size: 10})
	if got := u.SetTargetBitrate(50); got != 0 {
		t.Fatalf("unlimited generator set bitrate %v, want 0", got)
	}
}

func TestSizePacing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// 8 kbit/s is 1000 bytes per second, so a 100 byte write every 100 ms
		// once the one write burst is spent
		g := mustNew(t, Config{
			Pacing:   Size,
			Size:     100,
			Bounds:   mrtp.RateBounds{Initial: 8000, Max: 8000},
			Duration: time.Second,
		})
		records, err := collect(t, g, context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if len(records) != 10 {
			t.Fatalf("got %v writes, want 10", len(records))
		}
		for i, r := range records {
			if r.size != 100 {
				t.Errorf("write %v has %v bytes, want 100", i, r.size)
			}
			if want := time.Duration(i) * 100 * time.Millisecond; r.at != want {
				t.Errorf("write %v at %v, want %v", i, r.at, want)
			}
			if r.first != (i == 0) || r.burst != -1 || r.last {
				t.Errorf("write %v has first=%v burst=%v last=%v", i, r.first, r.burst, r.last)
			}
		}
	})
}

func TestSizePacingFollowsTargetBitrate(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		g := mustNew(t, Config{
			Pacing:   Size,
			Size:     100,
			Bounds:   mrtp.RateBounds{Initial: 8000, Max: 16000},
			Duration: time.Second,
		})
		var n int
		err := g.Run(context.Background(), func(Write) error {
			n++
			if n == 1 {
				g.SetTargetBitrate(16000)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		// 2000 bytes per second after the first write
		if n != 20 {
			t.Fatalf("got %v writes, want 20", n)
		}
	})
}

func TestUnlimitedRunsUntilCancelled(t *testing.T) {
	g := mustNew(t, Config{Pacing: Size, Size: 64, Random: true})
	ctx, cancel := context.WithCancel(context.Background())
	var n int
	var random bool
	err := g.Run(ctx, func(w Write) error {
		n++
		random = random || bytes.Count(w.Payload, []byte{0}) != len(w.Payload)
		if n == 1000 {
			cancel()
		}
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
	if n != 1000 {
		t.Fatalf("got %v writes, want 1000", n)
	}
	if !random {
		t.Fatal("payloads are all zeros, want random bytes")
	}
	if g.Active() {
		t.Fatal("generator is active after Run returned")
	}
}

func TestBursts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		g := mustNew(t, Config{
			Pacing: Size,
			Size:   1000,
			Burst:  &Burst{Bytes: 2500, Period: time.Second, Count: 3},
		})
		start := time.Now()
		var records []record
		err := g.Run(context.Background(), func(w Write) error {
			if !g.Active() {
				t.Error("generator is not active inside a burst")
			}
			records = append(records, record{
				size: len(w.Payload), burst: w.Burst, first: w.First, last: w.Last, at: time.Since(start),
			})
			if w.Burst == 0 && w.Last {
				// the first burst overruns the second's due time
				time.Sleep(1500 * time.Millisecond)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(records) != 9 {
			t.Fatalf("got %v writes, want 9", len(records))
		}
		// bursts are due at 1s, 2s and 3s, the second starts late at 2.5s
		starts := []time.Duration{time.Second, 2500 * time.Millisecond, 3 * time.Second}
		for i, r := range records {
			k, j := i/3, i%3
			wantSize := 1000
			if j == 2 {
				wantSize = 500
			}
			if r.burst != k || r.first != (j == 0) || r.last != (j == 2) || r.size != wantSize {
				t.Errorf("write %v: %+v", i, r)
			}
			if j == 0 && r.at != starts[k] {
				t.Errorf("burst %v starts at %v, want %v", k, r.at, starts[k])
			}
		}
		if g.Active() {
			t.Fatal("generator is active after the last burst")
		}
	})
}

func TestBurstsStopAtDuration(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		g := mustNew(t, Config{
			Pacing:   Size,
			Size:     10,
			Burst:    &Burst{Bytes: 10, Period: time.Second},
			Duration: 3500 * time.Millisecond,
		})
		records, err := collect(t, g, context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if len(records) != 3 {
			t.Fatalf("got %v bursts, want 3", len(records))
		}
	})
}

func TestStartDelay(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		g := mustNew(t, Config{
			Pacing:     Interval,
			FrameRate:  mrtp.FrameRate{Num: 1, Den: 1},
			Bounds:     mrtp.RateBounds{Initial: 8, Max: 8},
			Duration:   time.Second,
			StartDelay: 2 * time.Second,
		})
		start := time.Now()
		var at time.Duration
		err := g.Run(context.Background(), func(Write) error {
			at = time.Since(start)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if at != 2*time.Second {
			t.Fatalf("first write at %v, want 2s", at)
		}
	})
}

func TestEmitErrorStopsRun(t *testing.T) {
	g := mustNew(t, Config{Pacing: Size, Size: 1})
	want := errors.New("downstream failed")
	if err := g.Run(context.Background(), func(Write) error { return want }); !errors.Is(err, want) {
		t.Fatalf("got %v, want %v", err, want)
	}
}

func TestInvalidConfig(t *testing.T) {
	for name, config := range map[string]Config{
		"no frame rate":   {Pacing: Interval, Bounds: mrtp.RateBounds{Max: 1}},
		"no max bitrate":  {Pacing: Interval, FrameRate: mrtp.FrameRate{Num: 1, Den: 1}},
		"interval burst":  {Pacing: Interval, FrameRate: mrtp.FrameRate{Num: 1, Den: 1}, Bounds: mrtp.RateBounds{Max: 1}, Burst: &Burst{Bytes: 1, Period: 1}},
		"no size":         {Pacing: Size},
		"empty burst":     {Pacing: Size, Size: 1, Burst: &Burst{Period: 1}},
		"min above max":   {Pacing: Size, Size: 1, Bounds: mrtp.RateBounds{Min: 2, Max: 1}},
		"unknown pacing":  {Pacing: Pacing(42)},
		"negative period": {Pacing: Size, Size: 1, Burst: &Burst{Bytes: 1, Period: -1}},
	} {
		if _, err := New(config); err == nil {
			t.Errorf("%v: got no error", name)
		}
	}
}
