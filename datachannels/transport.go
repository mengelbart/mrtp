package datachannels

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"

	quicutils "github.com/mengelbart/mrtp/quic-utils"
	"github.com/mengelbart/quicdc"
	"github.com/quic-go/quic-go"
	quicgoqlog "github.com/quic-go/quic-go/qlog"
	"github.com/quic-go/quic-go/quicvarint"
)

const nextProto = "TODO"

type Transport struct {
	role          quicutils.Role
	localAddress  string
	remoteAddress string

	quicCC  int
	session *quicdc.Session
}

type Option func(*Transport) error

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
		role: quicutils.RoleServer,
	}

	for _, opt := range opts {
		if err := opt(t); err != nil {
			return nil, err
		}
	}

	if t.role == quicutils.RoleServer {
		c, err := quicutils.GenerateTLSConfig("", "", nil, []string{nextProto})
		if err != nil {
			return nil, err
		}
		t.session, err = accept(context.TODO(), t.localAddress, c, &quic.Config{
			EnableDatagrams:                true,
			InitialStreamReceiveWindow:     quicvarint.Max,
			InitialConnectionReceiveWindow: quicvarint.Max,
			CcType:                         quic.CCType(t.quicCC),
			SendTimestamps:                 false,
			Tracer:                         quicgoqlog.DefaultConnectionTracer,
		})
		if err != nil {
			return nil, err
		}
	} else {
		quicConn, err := quic.DialAddr(context.TODO(), t.remoteAddress, &tls.Config{
			InsecureSkipVerify: true,
			NextProtos:         []string{nextProto},
		}, &quic.Config{
			EnableDatagrams:                true,
			InitialStreamReceiveWindow:     quicvarint.Max,
			InitialConnectionReceiveWindow: quicvarint.Max,
			CcType:                         quic.CCType(t.quicCC),
			SendTimestamps:                 false,
			Tracer:                         quicgoqlog.DefaultConnectionTracer,
		})
		t.session = quicdc.NewSession(quicConn)
		if err != nil {
			return nil, err
		}
	}

	return t, nil
}

func (t *Transport) NewDataChannelSender(channelID uint64, priority uint32) (*Sender, error) {
	dc, err := t.session.OpenDataChannel(channelID, uint64(priority), true, 0, "", "")
	if err != nil {
		return nil, err
	}

	// SendMessage opens a stream
	mw, err := dc.SendMessage(context.TODO())
	if err != nil {
		return nil, err
	}

	return newSender(mw), nil
}

func (t *Transport) AddDataChannelReceiver(channelID uint64) (*Receiver, error) {

	dcCahn := make(chan *quicdc.DataChannel)
	t.session.OnIncomingDataChannel(func(dc *quicdc.DataChannel) {
		dcCahn <- dc
	})

	// start reader loop
	go t.session.Read()

	// wating for data channel from callback
	dc := <-dcCahn

	// open receiver stream
	rm, err := dc.ReceiveMessage(context.TODO())
	if err != nil {
		return nil, err
	}

	return newReceiver(rm), nil
}

func accept(ctx context.Context, addr string, tlsConfig *tls.Config, quicConfig *quic.Config) (*quicdc.Session, error) {
	listener, err := quic.ListenAddr(addr, tlsConfig, quicConfig)
	if err != nil {
		return nil, err
	}
	conn, err := listener.Accept(ctx)
	if err != nil {
		return nil, err
	}
	return quicdc.NewSession(conn), nil
}
