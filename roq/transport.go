package roq

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"log"
	"math/big"

	"github.com/go-gst/go-gst/gst"
	"github.com/mengelbart/qlog"
	"github.com/mengelbart/roq"
	"github.com/quic-go/quic-go"
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

func AddSender(flowID uint64) Option {
	return func(t *Transport) error {
		if _, ok := t.senders[flowID]; ok {
			return errors.New("duplicate sender flow ID")
		}
		t.senders[flowID] = nil
		return nil
	}
}

func AddReceiver(flowID uint64) Option {
	return func(t *Transport) error {
		if _, ok := t.receivers[flowID]; ok {
			return errors.New("duplicate receiver flow ID")
		}
		t.receivers[flowID] = nil
		return nil
	}
}

type Transport struct {
	role Role

	session   *roq.Session
	receivers map[uint64]*receiver
	senders   map[uint64]*sender
}

func New(opts ...Option) (*Transport, error) {
	t := &Transport{
		role:      RoleServer,
		session:   nil,
		receivers: map[uint64]*receiver{},
		senders:   map[uint64]*sender{},
	}

	for _, opt := range opts {
		if err := opt(t); err != nil {
			return nil, err
		}
	}

	var conn roq.Connection
	if t.role == RoleServer {
		c, err := generateTLSConfig("", "", nil)
		if err != nil {
			return nil, err
		}
		conn, err = accept(context.TODO(), "127.0.0.1:4242", c, &quic.Config{
			EnableDatagrams:                true,
			InitialStreamReceiveWindow:     quicvarint.Max,
			InitialConnectionReceiveWindow: quicvarint.Max,
			Tracer:                         quicgoqlog.DefaultConnectionTracer,
		})
		if err != nil {
			return nil, err
		}
	} else {
		quicConn, err := quic.DialAddr(context.TODO(), "127.0.0.1:4242", &tls.Config{
			InsecureSkipVerify: true,
			NextProtos:         []string{roqALPN},
		}, &quic.Config{
			EnableDatagrams:                true,
			InitialStreamReceiveWindow:     quicvarint.Max,
			InitialConnectionReceiveWindow: quicvarint.Max,
			Tracer:                         quicgoqlog.DefaultConnectionTracer,
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

	for id := range t.senders {
		flow, err := s.NewSendFlow(id)
		if err != nil {
			return nil, err
		}
		s, err := newSender(flow, SendModeDatagram)
		if err != nil {
			return nil, err
		}
		t.senders[id] = s
	}
	for id := range t.receivers {
		flow, err := s.NewReceiveFlow(id)
		if err != nil {
			return nil, err
		}
		r, err := newReceiver(flow)
		if err != nil {
			return nil, err
		}
		t.receivers[id] = r
	}

	t.session = s
	return t, nil
}

// GetSink implements mrtp.Transport.
func (t *Transport) GetSink(id int) *gst.Element {
	s, ok := t.senders[uint64(id)]
	if !ok {
		return nil
	}
	return s.element
}

// GetSrc implements mrtp.Transport.
func (t *Transport) GetSrc(id int) *gst.Element {
	r, ok := t.receivers[uint64(id)]
	if !ok {
		return nil
	}
	return r.element
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
