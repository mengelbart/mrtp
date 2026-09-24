package packetization

import (
	"testing"
	"time"

	"github.com/mengelbart/mrtp"
	"github.com/mengelbart/mrtp/pipeline"
	"github.com/pion/rtp"
)

// rtpCollector unmarshals and keeps every packet it is written.
type rtpCollector struct {
	packets []rtp.Packet
}

func (c *rtpCollector) Negotiate(mrtp.Format) error { return nil }
func (c *rtpCollector) EndOfStream() error          { return nil }
func (c *rtpCollector) Close() error                { return nil }

func (c *rtpCollector) Write(p mrtp.Packet[mrtp.RTPPacket]) error {
	defer p.Release()
	var packet rtp.Packet
	if err := packet.Unmarshal(append([]byte(nil), p.Value().Data...)); err != nil {
		return err
	}
	c.packets = append(c.packets, packet)
	return nil
}

func TestPacketizerFakeFrames(t *testing.T) {
	const (
		fps       = 7
		frames    = 20
		frameSize = 2500
		mtu       = 1212
	)
	p := NewRTPPacketizer(mtu, 96, 1, 90_000, mrtp.Fake)
	c := &rtpCollector{}
	if err := p.Negotiate(mrtp.EncodedVideo{Codec: mrtp.Fake}); err != nil {
		t.Fatal(err)
	}
	if err := p.Connect(c); err != nil {
		t.Fatal(err)
	}

	pool := pipeline.NewPool(
		func() *mrtp.EncodedFrame { return &mrtp.EncodedFrame{} },
		func(f *mrtp.EncodedFrame) { f.Data = f.Data[:0] },
	)
	var pts time.Duration
	for i := range frames {
		next := time.Duration(i+1) * time.Second / fps
		frame := pool.Get()
		frame.Value().Data = make([]byte, frameSize)
		frame.Value().PTS = pts
		frame.Value().Duration = next - pts
		if err := p.Write(frame); err != nil {
			t.Fatal(err)
		}
		pts = next
	}

	if n := pool.Outstanding(); n != 0 {
		t.Fatalf("%v frames were never released", n)
	}
	if n := p.pool.Outstanding(); n != 0 {
		t.Fatalf("%v packets were never released", n)
	}

	var frame int
	var bytes int
	ts0 := c.packets[0].Timestamp
	for i, packet := range c.packets {
		if len(packet.Payload) > mtu-12 {
			t.Errorf("packet %v has %v payload bytes, more than fits the MTU", i, len(packet.Payload))
		}
		// frame i starts at i/fps seconds, rounded down to the 90 kHz clock
		if want := ts0 + uint32(frame*90_000/fps); packet.Timestamp != want {
			t.Errorf("packet %v of frame %v has timestamp %v, want %v", i, frame, packet.Timestamp, want)
		}
		bytes += len(packet.Payload)
		if packet.Marker {
			if bytes != frameSize {
				t.Errorf("frame %v has %v payload bytes, want %v", frame, bytes, frameSize)
			}
			frame++
			bytes = 0
		}
	}
	if frame != frames {
		t.Fatalf("got %v frames, want %v", frame, frames)
	}
}
