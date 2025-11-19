package quictransport

import (
	"context"
	"time"

	"github.com/Willi-42/go-nada/nada"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/qlog"
	"github.com/quic-go/quic-go/qlogwriter"
)

type ReceivedCallback func(nada.Acknowledgment)
type OnLossEventFunc func(nada.Acknowledgment)

type RTT struct {
	lastRtt time.Duration
}

// getTimestamp returns ok=true if found timestmap, ok=false otherwise
func getTimestamp(frames []qlog.Frame) (sentTs uint64, ok bool) {
	// search for ts frame
	for _, f := range frames {
		if tsframe, ok := f.Frame.(*qlog.TimestampFrame); ok {
			return tsframe.Timestamp, true
		}
	}

	return 0, false
}

func catchRecvEvent(event qlogwriter.Event, tsCallback ReceivedCallback) {
	switch e := event.(type) {
	case qlog.PacketReceived:
		sentTs, ok := getTimestamp(e.Frames)
		if !ok {
			return
		}

		packet := nada.Acknowledgment{
			SeqNr:     uint64(e.Header.PacketNumber),
			Departure: time.UnixMicro(int64(sentTs)),
			Marked:    false, // TODO
			SizeBit:   uint64(e.Raw.Length) * 8,
			Arrival:   time.Now(),
			Arrived:   true,
		}

		// give the information back to the callback for the CC.
		tsCallback(packet)
	}
}

func catchRTTandLossEvent(event qlogwriter.Event, rtt *RTT, onLossEventFunc OnLossEventFunc) {
	switch e := event.(type) {
	case qlog.MetricsUpdated:
		rtt.lastRtt = e.LatestRTT
	case qlog.PacketLost:
		if onLossEventFunc != nil {
			onLossEventFunc(nada.Acknowledgment{SeqNr: uint64(e.Header.PacketNumber), Departure: time.Now()})
		}
	}
}

func receiveTracer(ctx context.Context, isClient bool, connID quic.ConnectionID, recvCallback ReceivedCallback, qlogFile string) qlogwriter.Trace {
	tsTracer := func(event qlogwriter.Event) {
		catchRecvEvent(event, recvCallback)
	}

	qlogTracer := createQlogTracer(ctx, isClient, connID, qlogFile)

	return newTracer(qlogTracer, tsTracer)
}

func senderTracers(
	ctx context.Context, isClient bool, connID quic.ConnectionID,
	onLossEvent OnLossEventFunc,
	lastRtt *RTT,
	qlogFile string,
) qlogwriter.Trace {
	rttLossTracer := func(event qlogwriter.Event) {
		catchRTTandLossEvent(event, lastRtt, onLossEvent)
	}

	qlogTracer := createQlogTracer(ctx, isClient, connID, qlogFile)

	return newTracer(qlogTracer, rttLossTracer)
}

func createQlogTracer(ctx context.Context, isClient bool, connID quic.ConnectionID, qlogFile string) qlogwriter.Trace {
	if qlogFile != "" {
		return qlog.DefaultConnectionTracerWithName(ctx, isClient, connID, qlogFile)
	}
	return qlog.DefaultConnectionTracer(ctx, isClient, connID)
}
