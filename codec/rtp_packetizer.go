package codec

import (
	"log/slog"
	"time"

	"github.com/mengelbart/mrtp/internal/logging"
	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
)

type RTPPacketizerFactory struct {
	MTU       uint16
	PT        uint8
	SSRC      uint32
	ClockRate uint32
}

type RTPPacketizer struct {
	MTU       uint16
	PT        uint8
	SSRC      uint32
	ClockRate uint32

	frameDuration time.Duration
	packetizer    rtp.Packetizer
	writer        Writer

	unwrapper *logging.Unwrapper // for logging the rtp packets
}

func (p *RTPPacketizerFactory) Link(w Writer, i Info) (Writer, error) {
	fps := float64(i.TimebaseNum) / float64(i.TimebaseDen)
	frameDuration := time.Duration(float64(time.Second) / fps)
	packetizer := rtp.NewPacketizer(p.MTU, p.PT, p.SSRC, &codecs.VP8Payloader{}, rtp.NewRandomSequencer(), p.ClockRate)
	return &RTPPacketizer{
		MTU:           p.MTU,
		PT:            p.PT,
		SSRC:          p.SSRC,
		ClockRate:     p.ClockRate,
		frameDuration: frameDuration,
		packetizer:    packetizer,
		writer:        w,
		unwrapper:     &logging.Unwrapper{},
	}, nil
}

func (p *RTPPacketizer) Write(encFrame []byte, a Attributes) error {
	samples := uint32(p.frameDuration.Seconds() * float64(p.ClockRate))
	pkts := p.packetizer.Packetize(encFrame, samples)
	pktBufs := make([][]byte, 0)

	// get PTS from attributes for logging
	var pts int64
	if ptsAttr, ok := a[PTS]; ok {
		if ptsVal, ok := ptsAttr.(int64); ok {
			pts = ptsVal
		}
	}

	for _, pkt := range pkts {
		buf, err := pkt.Marshal()
		if err != nil {
			return err
		}
		pktBufs = append(pktBufs, buf)

		// log packet
		slog.Info("rtp to pts mapping",
			"rtp-timestamp", pkt.Timestamp,
			"sequence-number", pkt.Header.SequenceNumber,
			"unwrapped-sequence-number", p.unwrapper.Unwrap(pkt.Header.SequenceNumber),
			"pts", pts,
		)
	}
	if writer, ok := p.writer.(MultiWriter); ok {
		if err := writer.WriteAll(pktBufs, a); err != nil {
			return err
		}
	} else {
		for _, pkt := range pktBufs {
			if err := p.writer.Write(pkt, a); err != nil {
				return err
			}
		}
	}
	return nil
}
