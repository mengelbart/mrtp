package rtp

import (
	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
)

type Packetizer struct {
	packetizer rtp.Packetizer
}

func NewPacketizer() *Packetizer {
	return &Packetizer{
		packetizer: rtp.NewPacketizer(1200, 96, 1, &codecs.VP8Payloader{}, rtp.NewRandomSequencer(), 90_000),
	}
}

func (r *Packetizer) Containerize(buf []byte) ([][]byte, error) {
	pkts := r.packetizer.Packetize(buf, 3000)
	res := make([][]byte, 0, len(pkts))
	for _, pkt := range pkts {
		pktBuf, err := pkt.Marshal()
		if err != nil {
			return nil, err
		}
		res = append(res, pktBuf)
	}
	return res, nil
}
