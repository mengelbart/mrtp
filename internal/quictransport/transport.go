package quictransport

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"slices"
	"sync/atomic"
	"time"

	"github.com/Willi-42/go-nada/nada"
	"github.com/pion/bwe/gcc"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/quicvarint"
)

type Option func(*Transport) error

// Transport is a quic connection that can be real-time congestion controlled.
// Opens a quic datachannel connection over it for the feedback.
// Only works with application data that use quicdc or roq format.
type Transport struct {
	ctx           context.Context
	netConn       net.PacketConn
	quicTransport *quic.Transport
	quicConn      *quic.Conn
	role          Role
	localAddress  string
	remoteAddress string

	running atomic.Bool

	pacingFactor    func() float64
	nada            *nada.SenderOnly
	bwe             *gcc.SendSideController
	lastBWEUpdate   time.Time
	inFlightPackets []packetFeedback
	lowestInFlight  uint64
	highestAcked    uint64
	packetFeedback  []packetFeedback

	qlogFile string

	SetSourceTargetRate func(ratebps uint) error
	HandleUniStream     func(flowID uint64, rs *quic.ReceiveStream)
	HandleDatagram      func(flowID uint64, datagram []byte)
}

func EnableNADA(initRate, minRate, maxRate, expectedFeedbackDelta uint) Option {
	return func(t *Transport) error {
		nadaConfig := nada.Config{
			MinRate:                  uint64(minRate),
			MaxRate:                  uint64(maxRate),
			StartRate:                uint64(initRate),
			FeedbackDelta:            uint64(expectedFeedbackDelta), // ms
			DeactivateQDelayWrapping: true,
			RefCongLevel:             15, // ms
		}

		nadaSo := nada.NewSenderOnly(nadaConfig)
		t.nada = &nadaSo
		return nil
	}
}

func EnableGCC(initRate, minRate, maxRate int) Option {
	return func(t *Transport) error {
		// TODO: add pion logger
		var err error
		t.bwe, err = gcc.NewSendSideController(initRate, minRate, maxRate)
		return err
	}
}

func WithRole(r Role) Option {
	return func(t *Transport) error {
		t.role = r
		return nil
	}
}

func SetNetConn(net net.PacketConn) Option {
	return func(t *Transport) error {
		t.netConn = net
		return nil
	}
}

func SetLocalAddress(address string, port uint) Option {
	return func(t *Transport) error {
		t.localAddress = fmt.Sprintf("%s:%d", address, port)
		return nil
	}
}

func SetRemoteAddress(address string, port uint) Option {
	return func(t *Transport) error {
		t.remoteAddress = fmt.Sprintf("%s:%d", address, port)
		return nil
	}
}

func EnableQLogs(qlogFile string) Option {
	return func(t *Transport) error {
		t.qlogFile = qlogFile
		return nil
	}
}

func PacingFactor(factor func() float64) Option {
	return func(t *Transport) error {
		t.pacingFactor = factor
		return nil
	}
}

func New(ctx context.Context, tlsNextProtos []string, opts ...Option) (*Transport, error) {
	t := &Transport{
		role:         RoleServer,
		ctx:          ctx,
		pacingFactor: func() float64 { return 1.0 },
	}

	for _, opt := range opts {
		if err := opt(t); err != nil {
			return nil, err
		}
	}

	tracer := &tracerFactory{
		qlogFileName: t.qlogFile,
		transport:    t,
	}

	if t.role == RoleServer {
		quicConfig := &quic.Config{
			EnableDatagrams: true,
			// InitialStreamReceiveWindow:     quicvarint.Max,
			InitialConnectionReceiveWindow: quicvarint.Max,
			MaxIncomingUniStreams:          quicvarint.Max,
			Tracer:                         tracer.newTracer,
		}

		var err error
		if t.netConn != nil {
			t.quicTransport, t.quicConn, err = OpenServerConnWithNet(ctx, quicConfig, tlsNextProtos, t.netConn)
		} else {
			t.quicConn, err = OpenServerConn(ctx, t.localAddress, quicConfig, tlsNextProtos)
		}
		if err != nil {
			return nil, err
		}
	} else {
		quicConfig := &quic.Config{
			EnableDatagrams: true,
			// InitialStreamReceiveWindow:     quicvarint.Max,
			InitialConnectionReceiveWindow: quicvarint.Max,
			MaxIncomingUniStreams:          quicvarint.Max,
			Tracer:                         tracer.newTracer,
		}

		var err error
		if t.netConn != nil {
			t.quicTransport, t.quicConn, err = OpenClientConnWithNet(ctx, t.remoteAddress, quicConfig, tlsNextProtos, t.netConn)
		} else {
			t.quicConn, err = OpenClientConn(ctx, t.remoteAddress, quicConfig, tlsNextProtos)
		}
		if err != nil {
			return nil, err
		}
	}

	t.running.Store(true)

	return t, nil
}

func (t *Transport) GetRTT() time.Duration {
	return t.quicConn.ConnectionStats().LatestRTT
}

func (t *Transport) StartHandlers() {
	go t.receiveDatagrams()
	go t.receiveUniStreams() // already opened feedback stream; do not have to worry about that here
}

// GetQuicDataChannel returns the underlying quic connection.
func (t *Transport) GetQuicConnection() *quic.Conn {
	return t.quicConn
}

// Close shuts down the transport and all associated goroutines.
func (t *Transport) Close() {
	t.running.Store(false)
	if t.quicConn != nil {
		t.quicConn.CloseWithError(0, "bye")
	}
	if t.quicTransport != nil {
		t.quicTransport.Close()
	}
}

func (t *Transport) receiveUniStreams() {
	for t.running.Load() {
		rs, err := t.quicConn.AcceptUniStream(t.ctx)
		if err != nil {
			slog.Error("Error in receiveUniStreams:", "error", err)
			return
		}

		// read flowID
		reader := quicvarint.NewReader(rs)
		flowID, err := quicvarint.Read(reader)
		if err != nil {
			var streamErr *quic.StreamError
			if errors.As(err, &streamErr) {
				// Stream was canceled; nothing to do
				continue
			}
			panic(err)
		}

		go func() {
			if t.HandleUniStream != nil {
				t.HandleUniStream(flowID, rs)
			}
		}()
	}
}

func (t *Transport) receiveDatagrams() {
	for t.running.Load() {
		dgram, err := t.quicConn.ReceiveDatagram(t.ctx)
		if err != nil {
			slog.Error("Error in receiveDatagrams:", "error", err)
			return
		}

		// read flowID
		flowID, _, err := quicvarint.Parse(dgram)
		if err != nil {
			panic(err)
		}

		if t.HandleDatagram != nil {
			t.HandleDatagram(flowID, dgram)
		}
	}
}

type packetFeedback struct {
	seqNr     uint64
	size      uint64
	arrived   bool
	departure time.Time
	arrival   time.Time
}

func (t *Transport) packetSent(ts time.Time, seqNr uint64, size int) {
	idx, ok := slices.BinarySearchFunc(t.inFlightPackets, seqNr, func(a packetFeedback, b uint64) int {
		return int(a.seqNr - b)
	})
	if !ok {
		t.inFlightPackets = slices.Insert(t.inFlightPackets, idx, packetFeedback{
			seqNr:     seqNr,
			size:      uint64(size),
			arrived:   false,
			departure: ts,
			arrival:   time.Time{},
		})
	}
}

func (t *Transport) packetLost(seqNr uint64) {
	if seqNr < t.lowestInFlight {
		return
	}
	idx, ok := slices.BinarySearchFunc(t.inFlightPackets, seqNr, func(a packetFeedback, b uint64) int {
		return int(a.seqNr - b)
	})
	if !ok {
		// lost packet that was already dropped from inflight list, nothing to do
		return
	}
	feedback := t.inFlightPackets[idx]
	t.inFlightPackets = slices.Delete(t.inFlightPackets, idx, idx+1)

	idx, ok = slices.BinarySearchFunc(t.packetFeedback, seqNr, func(a packetFeedback, b uint64) int {
		return int(a.seqNr - b)
	})
	if ok {
		t.packetFeedback[idx].arrived = false
	} else {
		t.packetFeedback = slices.Insert(t.packetFeedback, idx, feedback)
	}
}

func (t *Transport) packetAcked(ts time.Time, seqNr uint64, arrival time.Time) {
	if seqNr > t.highestAcked {
		t.highestAcked = seqNr
	}
	if seqNr < t.lowestInFlight {
		return
	}
	idx, ok := slices.BinarySearchFunc(t.inFlightPackets, seqNr, func(a packetFeedback, b uint64) int {
		return int(a.seqNr - b)
	})
	if !ok {
		// Acked packet that was already dropped from inflight list, nothing to do
		return
	}
	feedback := t.inFlightPackets[idx]
	feedback.arrived = true
	feedback.arrival = arrival
	t.inFlightPackets = slices.Delete(t.inFlightPackets, idx, idx+1)

	idx, ok = slices.BinarySearchFunc(t.packetFeedback, seqNr, func(a packetFeedback, b uint64) int {
		return int(a.seqNr - b)
	})
	if ok {
		t.packetFeedback[idx].arrived = true
		t.packetFeedback[idx].arrival = arrival
	} else {
		t.packetFeedback = slices.Insert(t.packetFeedback, idx, feedback)
	}
}

func (t *Transport) updateCongestionControl(ts time.Time) {
	var target uint
	if t.quicConn == nil {
		// connection not established yet, do not update sending rate
		return
	}
	if t.nada != nil {
		target = t.updateNADA()
	} else if t.bwe != nil {
		target = t.updateGCC(ts)
	}
	if target == 0 {
		// No congestion control update necessary
		return
	}
	t.lastBWEUpdate = time.Now()
	if t.SetSourceTargetRate != nil {
		if err := t.SetSourceTargetRate(target); err != nil {
			slog.Error("Error setting source target rate:", "error", err)
		}
	}
	t.quicConn.SetPacingRate(uint64(t.pacingFactor() * float64(target)))
}

func (t *Transport) updateNADA() uint {
	if time.Since(t.lastBWEUpdate) < 20*time.Millisecond {
		return 0
	}
	acks := []nada.Acknowledgment{}
	idx := 0
	for i, feedback := range t.packetFeedback {
		if feedback.seqNr > t.highestAcked {
			idx = i
			break
		}
		if feedback.seqNr < t.lowestInFlight {
			continue
		}
		// slog.Info("FEEDBACK", "seqNr", feedback.seqNr, "arrived", feedback.arrived, "departure", feedback.departure, "arrival", feedback.arrival, "rtt", feedback.arrival.Sub(feedback.departure).Milliseconds())
		acks = append(acks, nada.Acknowledgment{
			SeqNr:     feedback.seqNr,
			SizeBit:   feedback.size * 8,
			Departure: feedback.departure,
			Arrival:   feedback.arrival,
			Arrived:   feedback.arrived,
			Marked:    false, // TODO: ECN
		})
	}
	t.packetFeedback = t.packetFeedback[idx:]
	target := t.nada.OnAcks(t.quicConn.ConnectionStats().LatestRTT, acks)
	return uint(target)
}

func (t *Transport) updateGCC(ts time.Time) uint {
	if time.Since(t.lastBWEUpdate) < 20*time.Millisecond {
		return 0
	}
	idx := 0
	for i, feedback := range t.packetFeedback {
		if feedback.seqNr > t.highestAcked {
			idx = i
			break
		}
		if feedback.seqNr < t.lowestInFlight {
			continue
		}
		// slog.Info("FEEDBACK", "seqNr", feedback.seqNr, "arrived", feedback.arrived, "departure", feedback.departure, "arrival", feedback.arrival, "rtt", feedback.arrival.Sub(feedback.departure).Milliseconds())
		if feedback.arrived {
			t.bwe.OnAck(feedback.seqNr, int(feedback.size), feedback.departure, feedback.arrival)
		} else {
			t.bwe.OnLoss()
		}
	}
	t.packetFeedback = t.packetFeedback[idx:]
	t.lowestInFlight = t.highestAcked + 1
	conn := t.quicConn
	stats := conn.ConnectionStats()
	rtt := stats.LatestRTT
	target := t.bwe.OnFeedback(ts, rtt)
	return uint(target)
}
