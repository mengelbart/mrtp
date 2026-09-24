// Package rtp converts between encoded frames and RTP packets.
package rtp

import (
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/mengelbart/mrtp"
	"github.com/mengelbart/mrtp/internal/logging"
	"github.com/mengelbart/mrtp/pipeline"
	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
)

func getPacketizerByName(c mrtp.Codec) (rtp.Payloader, error) {
	switch c {
	case mrtp.VP8:
		return &codecs.VP8Payloader{}, nil
	case mrtp.VP9:
		return &codecs.VP9Payloader{}, nil
	case mrtp.H264:
		return &codecs.H264Payloader{}, nil
	case mrtp.Fake:
		// use G722 as 0s are a valid payload for it
		return &codecs.G722Payloader{}, nil
	}
	return nil, fmt.Errorf("unknown codec: %v", c)
}

// Packetizer turns encoded frames into RTP packets, one packet per Write
// downstream.
type Packetizer struct {
	MTU       uint16
	PT        uint8
	SSRC      uint32
	ClockRate uint32
	Codec     mrtp.Codec

	packetizer rtp.Packetizer
	pool       *pipeline.Pool[mrtp.RTPPacket]
	down       mrtp.Sink[mrtp.RTPPacket]

	unwrapper *logging.Unwrapper // for logging the rtp packets
}

func NewPacketizer(mtu uint16, pt uint8, ssrc, clockRate uint32, c mrtp.Codec) *Packetizer {
	return &Packetizer{
		MTU:       mtu,
		PT:        pt,
		SSRC:      ssrc,
		ClockRate: clockRate,
		Codec:     c,
		pool: pipeline.NewPool(
			func() *mrtp.RTPPacket { return &mrtp.RTPPacket{Data: make([]byte, mtu)} },
			func(p *mrtp.RTPPacket) { p.Data = p.Data[:cap(p.Data)] },
		),
		unwrapper: &logging.Unwrapper{},
	}
}

// Negotiate implements mrtp.Sink.
func (p *Packetizer) Negotiate(f mrtp.Format) error {
	encoded, ok := f.(mrtp.EncodedVideo)
	if !ok {
		return fmt.Errorf("RTP packetizer takes encoded video, not %v", f)
	}
	if encoded.Codec != p.Codec {
		return fmt.Errorf("RTP packetizer is configured for %v, not %v", p.Codec, encoded.Codec)
	}
	payloader, err := getPacketizerByName(p.Codec)
	if err != nil {
		return err
	}
	p.packetizer = rtp.NewPacketizer(p.MTU, p.PT, p.SSRC, payloader, rtp.NewRandomSequencer(), p.ClockRate)
	return nil
}

// Format implements mrtp.Source.
func (p *Packetizer) Format() mrtp.Format {
	return mrtp.RTP{
		Codec:       p.Codec,
		PayloadType: p.PT,
		SSRC:        p.SSRC,
		ClockRate:   p.ClockRate,
	}
}

// Connect implements mrtp.Source.
func (p *Packetizer) Connect(down mrtp.Sink[mrtp.RTPPacket]) error {
	if p.down != nil {
		return errors.New("rtp: packetizer is already connected")
	}
	p.down = down
	return nil
}

// Write implements mrtp.Sink.
func (p *Packetizer) Write(packet mrtp.Packet[mrtp.EncodedFrame]) error {
	defer packet.Release()

	if p.packetizer == nil {
		return errors.New("rtp: packetizer wrote before it was negotiated")
	}
	frame := packet.Value()
	// Rounding both ends of the frame to the clock keeps timestamps from
	// drifting when a frame is not a whole number of ticks long.
	samples := p.ticks(frame.PTS+frame.Duration) - p.ticks(frame.PTS)
	pts := frame.PTS.Microseconds()

	for _, pkt := range p.packetizer.Packetize(frame.Data, samples) {
		out := p.pool.Get()
		value := out.Value()
		size := pkt.MarshalSize()
		if cap(value.Data) < size {
			value.Data = make([]byte, size)
		}
		value.Data = value.Data[:size]
		if _, err := pkt.MarshalTo(value.Data); err != nil {
			out.Release()
			return err
		}

		slog.Debug("rtp to pts mapping",
			"rtp-timestamp", pkt.Timestamp,
			"sequence-number", pkt.SequenceNumber,
			"unwrapped-sequence-number", p.unwrapper.Unwrap(pkt.SequenceNumber),
			"pts", pts,
		)

		if err := p.down.Write(out); err != nil {
			return err
		}
	}
	return nil
}

// ticks converts d to RTP clock ticks.
func (p *Packetizer) ticks(d time.Duration) uint32 {
	clock := time.Duration(p.ClockRate)
	return uint32(d/time.Second*clock + d%time.Second*clock/time.Second)
}

// EndOfStream implements mrtp.Sink.
func (p *Packetizer) EndOfStream() error {
	return p.down.EndOfStream()
}

// Close implements mrtp.Element.
func (p *Packetizer) Close() error {
	return nil
}

var (
	_ mrtp.Sink[mrtp.EncodedFrame] = (*Packetizer)(nil)
	_ mrtp.Source[mrtp.RTPPacket]  = (*Packetizer)(nil)
)
