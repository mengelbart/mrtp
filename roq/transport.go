package roq

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"time"

	"github.com/Willi-42/go-nada/nada"
	"github.com/mengelbart/qlog"
	"github.com/mengelbart/roq"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/logging"
	quicgoqlog "github.com/quic-go/quic-go/qlog"
	"github.com/quic-go/quic-go/quicvarint"
)

const roqALPN = "roq-09"

type Role bool

const (
	RoleServer Role = true
	RoleClient Role = false
)

func (r Role) String() string {
	if r {
		return "Server"
	} else {
		return "client"
	}
}

type Option func(*Transport) error

func WithRole(r Role) Option {
	return func(t *Transport) error {
		t.role = r
		return nil
	}
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

		useNacks := false
		nadaSo := nada.NewSenderOnly(nadaConfig, useNacks)
		t.nada = &nadaSo
		t.lostPackets = NewPacketEvents()
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

type Transport struct {
	role    Role
	session *roq.Session

	localAddress  string
	remoteAddress string

	nada                 *nada.SenderOnly
	lastRTT              *RTT
	lostPackets          *PacketEvents
	receivedPackets      *PacketEvents
	SetTargetRateEncoder func(ratebps uint) error
	sendNadaFeedback     bool
	quicCC               int
}

func New(opts ...Option) (*Transport, error) {
	t := &Transport{
		role:            RoleServer,
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
		t.receivedPackets.AddEvent(ack)
	}

	var conn roq.Connection
	if t.role == RoleServer {
		c, err := generateTLSConfig("", "", nil)
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
				if t.nada != nil {
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
				if t.nada != nil {
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

	if t.nada != nil {
		go t.nadaFeedbackReceiver()
	}

	if t.sendNadaFeedback {
		go t.nadaFeedbackSender()
	}

	t.session = s
	return t, nil
}

// nadaFeedbackSender regularly sends the nada feedback.
// Splits it into several datagrams if the size is too large.
func (t *Transport) nadaFeedbackSender() {
	const maxEventsPerDatagram = 100 // Tune based on your measurements

	sendFlow, err := t.NewSendFlow(42, false)
	if err != nil {
		panic(err)
	}

	for {
		time.Sleep(100 * time.Millisecond)

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
	}
}

func (t *Transport) nadaFeedbackReceiver() {
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

		// register feedback with nada
		targetRate := t.nada.OnAcks(t.lastRTT.lastRtt, acks.PacketEvents)
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

func generateTLSConfig(certFile, keyFile string, keyLog io.Writer) (*tls.Config, error) {
	tlsConfig, err := generateTLSConfigWithCertAndKey(certFile, keyFile, keyLog)
	if err != nil {
		log.Printf("failed to generate TLS config from cert file and key, generating in memory certs: %v", err)
		tlsConfig, err = generateTLSConfigWithNewCert(keyLog)
	}
	return tlsConfig, err
}

func generateTLSConfigWithCertAndKey(certFile, keyFile string, keyLog io.Writer) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{roqALPN},
		KeyLogWriter: keyLog,
	}, nil
}

// Setup a bare-bones TLS config for the server
func generateTLSConfigWithNewCert(keyLog io.Writer) (*tls.Config, error) {
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		return nil, err
	}
	template := x509.Certificate{SerialNumber: big.NewInt(1)}
	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	tlsCert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		Certificates: []tls.Certificate{tlsCert},
		NextProtos:   []string{roqALPN},
		KeyLogWriter: keyLog,
	}, nil
}
