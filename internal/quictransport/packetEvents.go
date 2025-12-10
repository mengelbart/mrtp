package quictransport

import (
	"time"

	"github.com/Willi-42/go-nada/nada"
	"github.com/quic-go/quic-go/quicvarint"
)

func Marshal(eventChan chan nada.Acknowledgment, readLen int) ([]byte, error) {
	buf := make([]byte, 0)
	buf = quicvarint.Append(buf, uint64(readLen))

	for range readLen {
		p := <-eventChan
		arrivedTs := uint64(p.Arrival.UnixMicro())

		seqNr := p.SeqNr

		buf = quicvarint.Append(buf, seqNr)
		buf = quicvarint.Append(buf, arrivedTs)
		if p.Marked {
			buf = quicvarint.Append(buf, 1)
		} else {
			buf = quicvarint.Append(buf, 0)
		}
	}

	return buf, nil
}

func UnmarshalFeedback(buf []byte) ([]nada.Acknowledgment, error) {
	var err error
	packetEvents := make([]nada.Acknowledgment, 0)

	// read the number of packets
	numPackets, n, err := quicvarint.Parse(buf)
	if n < 0 {
		return nil, err
	}
	buf = buf[n:]

	for range numPackets {
		p := nada.Acknowledgment{
			Arrived: true,
		}

		p.SeqNr, n, err = quicvarint.Parse(buf)
		if n < 0 {
			return nil, err
		}
		buf = buf[n:]

		arivalMicro, n, err := quicvarint.Parse(buf)
		if n < 0 {
			return nil, err
		}

		p.Arrival = time.UnixMicro(int64(arivalMicro))
		buf = buf[n:]

		marked, n, err := quicvarint.Parse(buf)
		if n < 0 {
			return nil, err
		}
		buf = buf[n:]
		p.Marked = marked == 1

		packetEvents = append(packetEvents, p)
	}

	return packetEvents, nil
}
