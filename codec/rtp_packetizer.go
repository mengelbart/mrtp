package codec

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/mengelbart/mrtp/internal/logging"
	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
)

func getPacketizerByName(codec CodecType) (rtp.Payloader, error) {
	switch codec {
	case VP8:
		return &codecs.VP8Payloader{}, nil
	case VP9:
		return &codecs.VP9Payloader{}, nil
	}
	return nil, fmt.Errorf("unknown codec: %v", codec)
}

type RTPPacketizerFactory struct {
	MTU       uint16
	PT        uint8
	SSRC      uint32
	ClockRate uint32
	Codec     CodecType
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

	payloader, err := getPacketizerByName(p.Codec)
	if err != nil {
		return nil, err
	}

	packetizer := rtp.NewPacketizer(p.MTU, p.PT, p.SSRC, payloader, rtp.NewRandomSequencer(), p.ClockRate)
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
	pts, err := getPTS(a)
	if err != nil {
		return err
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
