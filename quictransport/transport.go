package quictransport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/Willi-42/go-nada/nada"
	"github.com/mengelbart/mrtp/datachannels"
	"github.com/mengelbart/mrtp/quicutils"
	"github.com/pion/bwe/gcc"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/logging"
	"github.com/quic-go/quic-go/quicvarint"
)

type Option func(*Transport) error

// Transport is a quic connection that can be real-time congestion controlled.
// Opens a quic datachannel connection over it for the feedback.
// Only works with application data that use quicdc or roq format.
type Transport struct {
	quicConn              *quic.Conn
	role                  quicutils.Role
	localAddress          string
	remoteAddress         string
	feedbackChannelFlowID uint64

	dcTransport *datachannels.Transport

	nada          *nada.SenderOnly
	bwe           *gcc.SendSideController
	feedbackDelta uint64 // ms

	lastRTT         *RTT
	lostPackets     chan nada.Acknowledgment
	receivedPackets chan nada.Acknowledgment

	sendNadaFeedback bool
	quicCC           int
	qlogWriter       io.WriteCloser

	SetSourceTargetRate func(ratebps uint) error
	HandleUintStream    func(flowID uint64, rs *quic.ReceiveStream)
	HandleDatagram      func(flowID uint64, datagram []byte)
}

func EnableNADA(initRate, minRate, maxRate, expectedFeedbackDelta uint, feedbackChannelFlowID uint64) Option {
	return func(t *Transport) error {
		t.feedbackChannelFlowID = feedbackChannelFlowID

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
		t.lostPackets = make(chan nada.Acknowledgment, 1000)
		return nil
	}
}

func EnableGCC(initRate, minRate, maxRate int, feedbackChannelFlowID uint64) Option {
	return func(t *Transport) error {
		// TODO: add pion logger
		var err error
		t.bwe, err = gcc.NewSendSideController(initRate, minRate, maxRate)
		t.lostPackets = make(chan nada.Acknowledgment, 1000)
		t.feedbackChannelFlowID = feedbackChannelFlowID
		return err
	}
}

func WithRole(r quicutils.Role) Option {
	return func(t *Transport) error {
		t.role = r
		return nil
	}
}

func SetQuicCC(quicCC int) Option {
	return func(t *Transport) error {
		if quicCC < 0 || quicCC > 2 {
			return errors.New("invalid quic CC value, must be 0, 1 or 2")
		}

		t.quicCC = quicCC
		return nil
	}
}

func EnableNADAfeedback(feedbackDelta, feedbackChannelFlowID uint64) Option {
	return func(t *Transport) error {
		t.feedbackChannelFlowID = feedbackChannelFlowID
		t.sendNadaFeedback = true
		t.receivedPackets = make(chan nada.Acknowledgment, 1000)
		t.feedbackDelta = feedbackDelta
		return nil
	}
}

func SetLocalAdress(address string, port uint) Option {
	return func(t *Transport) error {
		t.localAddress = fmt.Sprintf("%s:%d", address, port)
		return nil
	}
}

func SetRemoteAdress(address string, port uint) Option {
	return func(t *Transport) error {
		t.remoteAddress = fmt.Sprintf("%s:%d", address, port)
		return nil
	}
}

func EnableQLogs(qlogWriter io.WriteCloser) Option {
	return func(t *Transport) error {
		t.qlogWriter = qlogWriter
		return nil
	}
}

func New(tlsNextProtos []string, opts ...Option) (*Transport, error) {
	t := &Transport{
		role:            quicutils.RoleServer,
		lastRTT:         &RTT{},
		lostPackets:     nil,
		receivedPackets: nil,
		qlogWriter:      nil,
	}

	for _, opt := range opts {
		if err := opt(t); err != nil {
			return nil, err
		}
	}

	addLostPacket := func(ack nada.Acknowledgment) {
		select {
		case t.lostPackets <- ack:
			// Successfully added
		default:
			// quic-go drops the packet if tracer takes too long: "dos_prevention"
			slog.Info("quictransport packet nack dropped", "reason", "tracer buffered channel full")
		}
	}
	addReceivedPacket := func(ack nada.Acknowledgment) {
		select {
		case t.receivedPackets <- ack:
			// Successfully added
		default:
			// quic-go drops the packet if tracer takes too long: "dos_prevention"
			slog.Info("quictransport packet dropped", "reason", "tracer buffered channel full")
		}
	}

	pacerType := quic.DefaultPacer
	if t.nada != nil || t.bwe != nil {
		pacerType = quic.RatePacer // TODO: make configurable
	}

	if t.role == quicutils.RoleServer {
		quicConfig := &quic.Config{
			EnableDatagrams:                true,
			InitialStreamReceiveWindow:     quicvarint.Max,
			InitialConnectionReceiveWindow: quicvarint.Max,
			MaxIncomingUniStreams:          quicvarint.Max,
			CcType:                         quic.CCType(t.quicCC),
			PacerType:                      pacerType,
			SendTimestamps:                 true,
			Tracer: func(ctx context.Context, p logging.Perspective, id quic.ConnectionID) *logging.ConnectionTracer {
				if t.nada != nil || t.bwe != nil {
					return newSenderTracers(p, id, addLostPacket, t.lastRTT, t.qlogWriter)
				}
				if t.sendNadaFeedback {
					return receiveTracer(p, id, addReceivedPacket, t.qlogWriter)
				}
				return onlyQlogTracer(p, id, t.qlogWriter)
			},
		}

		var err error
		t.quicConn, err = quicutils.OpenServerConn(t.localAddress, quicConfig, tlsNextProtos)
		if err != nil {
			return nil, err
		}
	} else {
		quicConfig := &quic.Config{
			EnableDatagrams:                true,
			InitialStreamReceiveWindow:     quicvarint.Max,
			InitialConnectionReceiveWindow: quicvarint.Max,
			MaxIncomingUniStreams:          quicvarint.Max,
			CcType:                         quic.CCType(t.quicCC),
			PacerType:                      pacerType,
			SendTimestamps:                 true,
			Tracer: func(ctx context.Context, p logging.Perspective, id quic.ConnectionID) *logging.ConnectionTracer {
				if t.nada != nil || t.bwe != nil {
					return newSenderTracers(p, id, addLostPacket, t.lastRTT, t.qlogWriter)
				}
				if t.sendNadaFeedback {
					return receiveTracer(p, id, addReceivedPacket, t.qlogWriter)
				}
				return onlyQlogTracer(p, id, t.qlogWriter)
			},
		}

		var err error
		t.quicConn, err = quicutils.OpenClientConn(t.remoteAddress, quicConfig, tlsNextProtos)
		if err != nil {
			return nil, err
		}
	}

	// open datachannel connection for feedback
	if err := t.openDataChannelConn(); err != nil {
		return nil, err
	}

	if t.nada != nil || t.bwe != nil {
		go t.feedbackReceiver()
	}

	if t.sendNadaFeedback {
		go t.sendFeedback()
	}

	return t, nil
}

func (t *Transport) StartHandlers() {
	go t.receiveDatagrams()
	go t.receiveUniStreams() // already opened feedback stream; do not have to worry about that here
}

// GetQuicDataChannel returns the underlying quic connection.
func (t *Transport) GetQuicConnection() *quic.Conn {
	return t.quicConn
}

// GetQuicDataChannel returns the datachannel connection that was opend for the feedback.
func (t *Transport) GetQuicDataChannel() *datachannels.Transport {
	return t.dcTransport
}

func (t *Transport) receiveUniStreams() {
	haveFeedbackChannel := t.nada != nil || t.bwe != nil || t.sendNadaFeedback

	for {
		rs, err := t.quicConn.AcceptUniStream(context.Background())
		if err != nil {
			panic(err)
		}

		// read flowID
		reader := quicvarint.NewReader(rs)
		flowID, err := quicvarint.Read(reader)
		if err != nil {
			panic(err)
		}

		if haveFeedbackChannel && flowID == t.feedbackChannelFlowID {
			// register feedback channel with dc transport
			err := t.dcTransport.ReadStream(context.Background(), rs, flowID)
			if err != nil {
				panic(err)
			}

			continue
		}

		go func() {
			if t.HandleUintStream != nil {
				t.HandleUintStream(flowID, rs)
			}
		}()
	}
}

func (t *Transport) receiveDatagrams() {
	for {
		dgram, err := t.quicConn.ReceiveDatagram(context.TODO())
		if err != nil {
			panic(err)
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

func (t *Transport) openDataChannelConn() error {
	var err error
	t.dcTransport, err = datachannels.New(t.quicConn)
	if err != nil {
		return err
	}

	return nil
}

// sendFeedback regularly sends the nada/gcc feedback.
// Splits it into several datagrams if the size is too large.
func (t *Transport) sendFeedback() {
	const maxEventsPerDatagram = 100

	sendFlow, err := t.dcTransport.NewDataChannelSender(t.feedbackChannelFlowID, 0, false)
	if err != nil {
		panic(err)
	}

	for {
		time.Sleep(time.Duration(t.feedbackDelta) * time.Millisecond)
		lenChan := len(t.receivedPackets)

		// If small enough, send as-is
		if lenChan <= maxEventsPerDatagram {
			data, err := Marshal(t.receivedPackets, lenChan)
			if err != nil {
				panic(err)
			}
			_, err = sendFlow.Write(data)
			if err != nil {
				panic(err)
			}

			continue
		}

		// Split into batches
		for i := 0; i < lenChan; i += maxEventsPerDatagram {
			segmentSize := min(lenChan-i, maxEventsPerDatagram)

			data, err := Marshal(t.receivedPackets, segmentSize)
			if err != nil {
				panic(err)
			}

			_, err = sendFlow.Write(data)
			if err != nil {
				panic(err)
			}
		}
	}
}

func (t *Transport) feedbackReceiver() {
	feedbackFlow, err := t.dcTransport.AddDataChannelReceiver(t.feedbackChannelFlowID)
	if err != nil {
		panic(err)
	}

	buf := make([]byte, 4096)
	for {

		n, err := feedbackFlow.Read(buf)
		if err != nil {
			panic(err)
		}

		acks, err := UnmarshalFeedback(buf[:n])
		if err != nil {
			panic(err)
		}

		// append losses/nacks
		lossCount := len(t.lostPackets)
		for range lossCount {
			nack := <-t.lostPackets
			acks = append(acks, nack)
		}

		// register feedback with cc
		var targetRate uint
		if t.nada != nil {
			targetRate = uint(t.nada.OnAcks(t.lastRTT.lastRtt, acks))
		}
		if t.bwe != nil {
			for _, pe := range acks {
				if pe.Arrived {
					t.bwe.OnAck(pe.SeqNr, int(pe.SizeBit/8), pe.Departure, pe.Arrival)
				} else {
					t.bwe.OnLoss()
				}
			}
			targetRate = uint(t.bwe.OnFeedback(time.Now(), t.lastRTT.lastRtt))
		}

		if t.SetSourceTargetRate != nil {
			// rate for source
			t.SetSourceTargetRate(targetRate)

			// rate for pacer
			rateByte := uint(float64(targetRate) / 8 * 1.2) // TODO
			t.quicConn.SetPacerRate(rateByte)
		}
	}
}
