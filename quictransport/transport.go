package quictransport

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Willi-42/go-nada/nada"
	"github.com/mengelbart/mrtp/datachannels"
	"github.com/mengelbart/mrtp/quicutils"
	"github.com/pion/bwe-test/gcc"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/logging"
	quicgoqlog "github.com/quic-go/quic-go/qlog"
	"github.com/quic-go/quic-go/quicvarint"
)

const feedbackChannelID = 0

type Option func(*Transport) error

// Transport is a quic connection that can be real-time congestion controlled.
// Opens a quic datachannel connection over it for the feedback.
// Only works with application data that use quicdc or roq format.
type Transport struct {
	quicConn      *quic.Conn
	role          quicutils.Role
	localAddress  string
	remoteAddress string

	dcTransport *datachannels.Transport

	nada          *nada.SenderOnly
	bwe           *gcc.SendSideController
	feedbackDelta uint64 // ms

	lastRTT          *RTT
	lostPackets      *PacketEvents
	receivedPackets  *PacketEvents
	sendNadaFeedback bool
	quicCC           int
	mtx              sync.Mutex

	SetSourceTargetRate func(ratebps uint) error
	HandleUintStream    func(flowID uint64, rs *quic.ReceiveStream)
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
		}

		useNacks := true
		nadaSo := nada.NewSenderOnly(nadaConfig, useNacks)
		t.nada = &nadaSo
		t.lostPackets = NewPacketEvents()
		return nil
	}
}

func EnableGCC(initRate, minRate, maxRate int) Option {
	return func(t *Transport) error {
		// TODO: add pion logger
		var err error
		t.bwe, err = gcc.NewSendSideController(initRate, minRate, maxRate)
		t.lostPackets = NewPacketEvents()
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

func EnableNADAfeedback(feedbackDelta uint64) Option {
	return func(t *Transport) error {
		t.sendNadaFeedback = true
		t.receivedPackets = NewPacketEvents()
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

func New(tlsNextProtos []string, opts ...Option) (*Transport, error) {
	t := &Transport{
		role:            quicutils.RoleServer,
		lastRTT:         &RTT{},
		lostPackets:     nil,
		receivedPackets: nil,
	}

	for _, opt := range opts {
		if err := opt(t); err != nil {
			return nil, err
		}
	}

	addLostPacket := func(ack nada.Acknowledgment) {
		t.lostPackets.AddEvent(ack)
	}
	addReceivedPacket := func(ack nada.Acknowledgment) {
		t.mtx.Lock()
		defer t.mtx.Unlock()
		t.receivedPackets.AddEvent(ack)
	}

	if t.role == quicutils.RoleServer {
		quicConfig := &quic.Config{
			EnableDatagrams:                true,
			InitialStreamReceiveWindow:     quicvarint.Max,
			InitialConnectionReceiveWindow: quicvarint.Max,
			MaxIncomingUniStreams:          quicvarint.Max,
			CcType:                         quic.CCType(t.quicCC),
			SendTimestamps:                 true,
			Tracer: func(ctx context.Context, p logging.Perspective, id quic.ConnectionID) *logging.ConnectionTracer {
				if t.nada != nil || t.bwe != nil {
					return newSenderTracers(p, id, addLostPacket, t.lastRTT)
				}
				if t.sendNadaFeedback {
					return receiveTracer(p, id, addReceivedPacket)
				}
				return quicgoqlog.DefaultConnectionTracer(context.TODO(), p, id)
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
			SendTimestamps:                 true,
			Tracer: func(ctx context.Context, p logging.Perspective, id quic.ConnectionID) *logging.ConnectionTracer {
				if t.nada != nil || t.bwe != nil {
					return newSenderTracers(p, id, addLostPacket, t.lastRTT)
				}
				if t.sendNadaFeedback {
					return receiveTracer(p, id, addReceivedPacket)
				}
				return quicgoqlog.DefaultConnectionTracer(context.TODO(), p, id)
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

		if flowID == feedbackChannelID {
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

	sendFlow, err := t.dcTransport.NewDataChannelSender(feedbackChannelID, 0)
	if err != nil {
		panic(err)
	}

	for {
		time.Sleep(time.Duration(t.feedbackDelta) * time.Millisecond)
		t.mtx.Lock()

		// If small enough, send as-is
		if len(t.receivedPackets.PacketEvents) <= maxEventsPerDatagram {
			data, err := t.receivedPackets.Marshal()
			if err != nil {
				panic(err)
			}
			_, err = sendFlow.Write(data)
			if err != nil {
				panic(err)
			}

			// Clear the event history
			t.receivedPackets.Empty()
			t.mtx.Unlock()
			continue
		}

		// Split into batches
		for i := 0; i < len(t.receivedPackets.PacketEvents); i += maxEventsPerDatagram {
			end := min(i+maxEventsPerDatagram, len(t.receivedPackets.PacketEvents))

			batch := &PacketEvents{
				PacketEvents: t.receivedPackets.PacketEvents[i:end],
			}

			data, err := batch.Marshal()
			if err != nil {
				panic(err)
			}

			_, err = sendFlow.Write(data)
			if err != nil {
				panic(err)
			}

		}

		// Clear the event history
		t.receivedPackets.Empty()
		t.mtx.Unlock()
	}
}

func (t *Transport) feedbackReceiver() {
	feedbackFlow, err := t.dcTransport.AddDataChannelReceiver(feedbackChannelID)
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

		// append losses
		acks.PacketEvents = append(acks.PacketEvents, t.lostPackets.PacketEvents...)
		t.lostPackets.Empty()

		// register feedback with cc
		var targetRate uint
		if t.nada != nil {
			targetRate = uint(t.nada.OnAcks(t.lastRTT.lastRtt, acks.PacketEvents))
		}
		if t.bwe != nil {
			gccAcks := acks.getGCCacks()
			targetRate = uint(t.bwe.OnAcks(time.Now(), t.lastRTT.lastRtt, gccAcks))
		}

		if t.SetSourceTargetRate != nil {
			t.SetSourceTargetRate(targetRate)
		}
	}
}
