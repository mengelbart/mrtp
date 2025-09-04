package quictransport

import (
	"context"
	"time"

	"github.com/Willi-42/go-nada/nada"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/logging"
	quicgoqlog "github.com/quic-go/quic-go/qlog"
)

type TimestampCallback func(nada.Acknowledgment)
type OnLossEventFunc func(nada.Acknowledgment)

type RTT struct {
	lastRtt time.Duration
}

// getTimestamp returns ok=true if found timestmap, ok=false otherwise
func getTimestamp(frames []logging.Frame) (sentTs uint64, ok bool) {
	// search for ts frame
	sentTs = uint64(0)
	ok = false

	// get Timestamp and check for data
	for _, f := range frames {
		switch frame := f.(type) {
		case *logging.TimestampFrame:
			sentTs = frame.Timestamp
			ok = true
		}
	}

	return sentTs, ok
}

func newTsTracer(tsCallback TimestampCallback) *logging.ConnectionTracer {
	return &logging.ConnectionTracer{
		ReceivedShortHeaderPacket: func(
			header *logging.ShortHeader,
			byteCnt logging.ByteCount,
			ecn logging.ECN,
			frames []logging.Frame,
		) {
			sentTs, ok := getTimestamp(frames)
			if !ok {
				return
			}

			packet := nada.Acknowledgment{
				SeqNr:     uint64(header.PacketNumber),
				Departure: time.UnixMicro(int64(sentTs)),
				Marked:    false, // TODO
				SizeBit:   uint64(byteCnt) * 8,
				Arrival:   time.Now(),
				Arrived:   true,
			}

			// give the information back to the callback for the CC.
			tsCallback(packet)
		},
	}
}

func receiveTracer(p logging.Perspective, id quic.ConnectionID, tsCallback TimestampCallback) *logging.ConnectionTracer {

	qlogTracer := quicgoqlog.DefaultConnectionTracer(context.TODO(), p, id)

	tracers := []*logging.ConnectionTracer{newTsTracer(tsCallback)}
	if qlogTracer != nil {
		tracers = append(tracers, qlogTracer)
	}

	return logging.NewMultiplexedConnectionTracer(tracers...)
}

func newRttAndLossTracer(rtt *RTT, onLossEventFunc OnLossEventFunc) *logging.ConnectionTracer {
	return &logging.ConnectionTracer{
		UpdatedMetrics: func(
			rttStats *logging.RTTStats,
			cwnd, bytesInFlight logging.ByteCount,
			packetsInFlight int,
		) {
			rtt.lastRtt = rttStats.LatestRTT()
		},
		LostPacket: func(_ logging.EncryptionLevel, pn logging.PacketNumber, _ logging.PacketLossReason) {
			if onLossEventFunc != nil {
				onLossEventFunc(nada.Acknowledgment{SeqNr: uint64(pn), Departure: time.Now()})
			}
		},
	}
}

func newSenderTracers(
	p logging.Perspective,
	id quic.ConnectionID,
	onLossEvent OnLossEventFunc,
	lastRtt *RTT,
) *logging.ConnectionTracer {
	rttLossTracer := newRttAndLossTracer(lastRtt, onLossEvent)
	tracers := []*logging.ConnectionTracer{rttLossTracer}

	qlogTracer := quicgoqlog.DefaultConnectionTracer(context.TODO(), p, id)
	if qlogTracer != nil {
		tracers = append(tracers, qlogTracer)
	}

	return logging.NewMultiplexedConnectionTracer(tracers...)
}
