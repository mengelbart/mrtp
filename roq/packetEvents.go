package roq

import (
	"time"

	"github.com/Willi-42/go-nada/nada"
	"github.com/quic-go/quic-go/quicvarint"
)

type PacketEvents struct {
	PacketEvents []nada.Acknowledgment
}

func NewPacketEvents() *PacketEvents {
	return &PacketEvents{
		PacketEvents: make([]nada.Acknowledgment, 0),
	}
}

func (ps *PacketEvents) AddEvent(p nada.Acknowledgment) {
	ps.PacketEvents = append(ps.PacketEvents, p)
}

func (ps *PacketEvents) Empty() {
	ps.PacketEvents = make([]nada.Acknowledgment, 0)
}

func (ps *PacketEvents) Marshal() ([]byte, error) {
	buf := make([]byte, 0)
	buf = quicvarint.Append(buf, uint64(len(ps.PacketEvents)))
	firstTs := uint64(0)

	gotFirstSeqNr := false
	firstSeqNr := uint64(0)

	for _, p := range ps.PacketEvents {
		deparuredTs := uint64(p.Departure.UnixMicro())
		arrivedTs := uint64(p.Arrival.UnixMicro())
		owd := arrivedTs - deparuredTs

		if firstTs == 0 {
			firstTs = deparuredTs
		} else {
			deparuredTs = deparuredTs - firstTs
		}

		seqNr := p.SeqNr
		if !gotFirstSeqNr {
			firstSeqNr = seqNr
			gotFirstSeqNr = true
		} else {
			seqNr = seqNr - firstSeqNr
		}

		buf = quicvarint.Append(buf, seqNr)
		buf = quicvarint.Append(buf, deparuredTs)
		buf = quicvarint.Append(buf, owd)
		buf = quicvarint.Append(buf, p.SizeBit)
		if p.Marked {
			buf = quicvarint.Append(buf, 1)
		} else {
			buf = quicvarint.Append(buf, 0)
		}
	}

	return buf, nil
}

func UnmarshalFeedback(buf []byte) (PacketEvents, error) {
	var ps PacketEvents
	var err error

	ps.PacketEvents = make([]nada.Acknowledgment, 0)
	firstTs := uint64(0)

	// read the number of packets
	numPackets, n, err := quicvarint.Parse(buf)
	if n < 0 {
		return PacketEvents{}, err
	}
	buf = buf[n:]

	gotFirstSeqNr := false
	firstSeqNr := uint64(0)

	for range numPackets {
		p := nada.Acknowledgment{
			Arrived: true,
		}

		p.SeqNr, n, err = quicvarint.Parse(buf)
		if n < 0 {
			return PacketEvents{}, err
		}
		buf = buf[n:]

		if !gotFirstSeqNr {
			firstSeqNr = p.SeqNr
			gotFirstSeqNr = true
		} else {
			p.SeqNr = p.SeqNr + firstSeqNr
		}

		departureMicro, n, err := quicvarint.Parse(buf)
		if n < 0 {
			return PacketEvents{}, err
		}
		if firstTs == 0 {
			firstTs = departureMicro
		} else {
			departureMicro = departureMicro + firstTs
		}

		p.Departure = time.UnixMicro(int64(departureMicro))
		buf = buf[n:]

		owd, n, err := quicvarint.Parse(buf)
		if n < 0 {
			return PacketEvents{}, err
		}
		p.Arrival = time.UnixMicro(int64(departureMicro + owd))
		buf = buf[n:]

		p.SizeBit, n, err = quicvarint.Parse(buf)
		if n < 0 {
			return PacketEvents{}, err
		}
		buf = buf[n:]

		marked, n, err := quicvarint.Parse(buf)
		if n < 0 {
			return PacketEvents{}, err
		}
		buf = buf[n:]
		p.Marked = marked == 1

		ps.PacketEvents = append(ps.PacketEvents, p)
	}

	return ps, nil
}
