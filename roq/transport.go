package roq

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Willi-42/go-nada/nada"
	quicutils "github.com/mengelbart/mrtp/quic-utils"
	"github.com/mengelbart/qlog"
	"github.com/mengelbart/roq"
	"github.com/pion/bwe-test/gcc"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/logging"
	quicgoqlog "github.com/quic-go/quic-go/qlog"
	"github.com/quic-go/quic-go/quicvarint"
)

const roqALPN = "roq-09"

type Option func(*Transport) error

func WithRole(r quicutils.Role) Option {
	return func(t *Transport) error {
		t.role = r
		return nil
	}
}

type Transport struct {
	role    quicutils.Role
	session *roq.Session

	localAddress  string
	remoteAddress string

	nada                 *nada.SenderOnly
	bwe                  *gcc.SendSideController
	lastRTT              *RTT
	lostPackets          *PacketEvents
	receivedPackets      *PacketEvents
	SetTargetRateEncoder func(ratebps uint) error
	sendNadaFeedback     bool
	quicCC               int
	mtx                  sync.Mutex
}

func EnableNADA(initRate, minRate, maxRate int) Option {
	return func(t *Transport) error {
		nadaConfig := nada.Config{
			MinRate:                  uint64(minRate),
			MaxRate:                  uint64(maxRate),
			StartRate:                uint64(initRate),
			FeedbackDelta:            100, // ms
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

func SetQuicCC(quicCC int) Option {
	return func(t *Transport) error {
		if quicCC < 0 || quicCC > 2 {
			return errors.New("invalid quic CC value, must be 0, 1 or 2")
		}

		t.quicCC = quicCC
		return nil
	}
}

func EnableNADAfeedback() Option {
	return func(t *Transport) error {
		t.sendNadaFeedback = true
		t.receivedPackets = NewPacketEvents()
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

func New(opts ...Option) (*Transport, error) {
	t := &Transport{
		role:            quicutils.RoleServer,
		session:         nil,
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

	var conn roq.Connection
	if t.role == quicutils.RoleServer {
		c, err := quicutils.GenerateTLSConfig("", "", nil, []string{roqALPN})
		if err != nil {
			return nil, err
		}
		conn, err = accept(context.TODO(), t.localAddress, c, &quic.Config{
			EnableDatagrams:                true,
			InitialStreamReceiveWindow:     quicvarint.Max,
			InitialConnectionReceiveWindow: quicvarint.Max,
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
		})
		if err != nil {
			return nil, err
		}
	} else {
		quicConn, err := quic.DialAddr(context.TODO(), t.remoteAddress, &tls.Config{
			InsecureSkipVerify: true,
			NextProtos:         []string{roqALPN},
		}, &quic.Config{
			EnableDatagrams:                true,
			InitialStreamReceiveWindow:     quicvarint.Max,
			InitialConnectionReceiveWindow: quicvarint.Max,
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
		})
		conn = roq.NewQUICGoConnection(quicConn)
		if err != nil {
			return nil, err
		}
	}

	// ql := qlog.NewQLOGHandler(os.Stdout, "qlog", "qlog", t.role.String())
	ql := (*qlog.Logger)(nil)
	s, err := roq.NewSession(conn, true, ql)
	if err != nil {
		return nil, err
	}

	if t.nada != nil || t.bwe != nil {
		go t.feedbackReceiver()
	}

	if t.sendNadaFeedback {
		go t.sendFeedback()
	}

	t.session = s
	return t, nil
}

// sendFeedback regularly sends the nada/gcc feedback.
// Splits it into several datagrams if the size is too large.
func (t *Transport) sendFeedback() {
	const maxEventsPerDatagram = 100 // Tune based on your measurements

	sendFlow, err := t.NewSendFlow(42, false)
	if err != nil {
		panic(err)
	}

	for {
		time.Sleep(100 * time.Millisecond)
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
	feedbackFlow, err := t.NewReceiveFlow(42, false)
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
		var targetRate uint64
		if t.nada != nil {
			targetRate = t.nada.OnAcks(t.lastRTT.lastRtt, acks.PacketEvents)
		}
		if t.bwe != nil {
			gccAcks := acks.getGCCacks()
			targetRate = uint64(t.bwe.OnAcks(time.Now(), t.lastRTT.lastRtt, gccAcks))
		}

		// set target rate of encoder
		if t.SetTargetRateEncoder != nil {
			t.SetTargetRateEncoder(uint(targetRate))
		}
	}
}

func (t *Transport) NewSendFlow(id uint64, logRTPpackets bool) (*Sender, error) {
	flow, err := t.session.NewSendFlow(id)
	if err != nil {
		return nil, err
	}
	return newSender(flow, SendModeDatagram, logRTPpackets)
}

func (t *Transport) NewReceiveFlow(id uint64, logRTPpackets bool) (*Receiver, error) {
	flow, err := t.session.NewReceiveFlow(id)
	if err != nil {
		return nil, err
	}
	return newReciever(flow, logRTPpackets), nil
}

func accept(ctx context.Context, addr string, tlsConfig *tls.Config, quicConfig *quic.Config) (*roq.QUICGoConnection, error) {
	listener, err := quic.ListenAddr(addr, tlsConfig, quicConfig)
	if err != nil {
		return nil, err
	}
	conn, err := listener.Accept(ctx)
	if err != nil {
		return nil, err
	}
	return roq.NewQUICGoConnection(conn), nil
}
