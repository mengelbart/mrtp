package fake

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mengelbart/mrtp"
	"github.com/mengelbart/mrtp/media"
)

// frameCollector keeps the size, PTS and duration of every frame it is written.
type frameCollector struct {
	frames []mrtp.EncodedFrame
	eos    int
}

func (c *frameCollector) Negotiate(mrtp.Format) error { return nil }
func (c *frameCollector) EndOfStream() error          { c.eos++; return nil }
func (c *frameCollector) Close() error                { return nil }

func (c *frameCollector) Write(p mrtp.Packet[mrtp.EncodedFrame]) error {
	defer p.Release()
	f := *p.Value()
	f.Data = make([]byte, len(f.Data))
	c.frames = append(c.frames, f)
	return nil
}

func TestSourceFrames(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// 7 fps for one second at 56 kbit/s is 7 frames of 1000 bytes
		src, err := New(time.Second, 7, media.RateBounds{Initial: 56_000, Min: 1, Max: 1_000_000})
		if err != nil {
			t.Fatal(err)
		}
		c := &frameCollector{}
		if err = src.Connect(c); err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		if err = src.Run(context.Background()); err != nil {
			t.Fatal(err)
		}

		if len(c.frames) != 7 || c.eos != 1 {
			t.Fatalf("got %v frames and %v ends of stream, want 7 and 1", len(c.frames), c.eos)
		}
		var end time.Duration
		for i, f := range c.frames {
			if len(f.Data) != 1000 {
				t.Errorf("frame %v has %v bytes, want 1000", i, len(f.Data))
			}
			if f.PTS != end {
				t.Errorf("frame %v starts at %v, want the previous frame's end %v", i, f.PTS, end)
			}
			end = f.PTS + f.Duration
		}
		if end != time.Second {
			t.Errorf("frames end at %v, want 1s", end)
		}
		// the last frame is due at 6/7 s
		if elapsed := time.Since(start); elapsed != 6*time.Second/7 {
			t.Errorf("run took %v, want %v", elapsed, 6*time.Second/7)
		}
	})
}

func TestSetTargetBitrateClamps(t *testing.T) {
	src, err := New(time.Second, 30, media.RateBounds{Initial: 5, Min: 100, Max: 200})
	if err != nil {
		t.Fatal(err)
	}
	if got := src.bitrate.Load(); got != 100 {
		t.Fatalf("initial bitrate is %v, want it clamped to 100", got)
	}
	if err = src.SetTargetBitrate(1000); err != nil {
		t.Fatal(err)
	}
	if got := src.bitrate.Load(); got != 200 {
		t.Fatalf("bitrate is %v, want it clamped to 200", got)
	}
}
