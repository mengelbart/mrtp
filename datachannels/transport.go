package datachannels

import (
	"context"
	"errors"
	"fmt"

	"github.com/mengelbart/mrtp/quicutils"
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

	quicConn *quic.Conn
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

func SetExistingQuicConn(conn *quic.Conn) Option {
	return func(t *Transport) error {
		t.quicConn = conn
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

	if t.quicConn != nil {
		println("quicdc: use existing quic connection")
		// use existing quic connection
		t.session = quicdc.NewSession(t.quicConn)
		return t, nil
	}

	if t.role == quicutils.RoleServer {
		conf := &quic.Config{
			EnableDatagrams:                true,
			InitialStreamReceiveWindow:     quicvarint.Max,
			InitialConnectionReceiveWindow: quicvarint.Max,
			CcType:                         quic.CCType(t.quicCC),
			SendTimestamps:                 false,
			Tracer:                         quicgoqlog.DefaultConnectionTracer,
		}

		var err error
		t.quicConn, err = quicutils.OpenServerConn(t.localAddress, conf, []string{nextProto})
		if err != nil {
			return nil, err
		}
	} else {
		conf := &quic.Config{
			EnableDatagrams:                true,
			InitialStreamReceiveWindow:     quicvarint.Max,
			InitialConnectionReceiveWindow: quicvarint.Max,
			CcType:                         quic.CCType(t.quicCC),
			SendTimestamps:                 false,
			Tracer:                         quicgoqlog.DefaultConnectionTracer,
		}
		var err error
		t.quicConn, err = quicutils.OpenClientConn(t.remoteAddress, conf, []string{nextProto})
		if err != nil {
			return nil, err
		}
	}

	t.session = quicdc.NewSession(t.quicConn)

	return t, nil
}

func (t *Transport) NewDataChannelSender(channelID uint64, priority uint32) (*Sender, error) {
	dc, err := t.session.OpenDataChannel(channelID, uint64(priority), true, 0, "", "")
	if err != nil {
		return nil, err
	}

	return newSender(dc), nil
}

// ReadStream registers a QUIC stream to the quicdc session
func (t *Transport) ReadStream(ctx context.Context, stream *quic.ReceiveStream, channelID uint64) error {
	return t.session.ReadStream(ctx, stream, channelID)
}

func (t *Transport) AddDataChannelReceiver(channelID uint64) (*Receiver, error) {

	// TODO: incorrect: this sets a handler. If AddDataChannelReceiver is called again, it overwrites the previous handler
	dcCahn := make(chan *quicdc.DataChannel)
	t.session.OnIncomingDataChannel(func(dc *quicdc.DataChannel) {
		dcCahn <- dc
	})

	// wating for data channel from callback
	dc := <-dcCahn

	return newReceiver(dc), nil
}
