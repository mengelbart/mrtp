package quicutils

import (
	"context"
	"crypto/tls"
	"sync"

	"github.com/quic-go/quic-go"
)

type QUICConnHandler interface {
	Handle(*quic.Conn)
}

type Listener struct {
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	Handler QUICConnHandler
}

func NewListener(h QUICConnHandler) *Listener {
	ctx, cancel := context.WithCancel(context.Background())
	return &Listener{
		Handler: h,
		ctx:     ctx,
		cancel:  cancel,
		wg:      sync.WaitGroup{},
	}
}

func (l *Listener) ListenAndHandle(localAddress string, quicConfig *quic.Config, tlsNextProtos []string) error {
	tlsConfig, err := GenerateTLSConfig("", "", nil, tlsNextProtos)
	if err != nil {
		return err
	}
	listener, err := quic.ListenAddr(localAddress, tlsConfig, quicConfig)
	if err != nil {
		return err
	}
	for {
		conn, err := listener.Accept(l.ctx)
		if err != nil {
			return err
		}
		l.wg.Go(func() {
			l.Handler.Handle(conn)
		})
	}
}

func (l *Listener) Close() error {
	l.cancel()
	l.wg.Wait()
	return nil
}

func OpenServerConn(localAddress string, quicConfig *quic.Config, tlsNextProtos []string) (*quic.Conn, error) {
	tlsConfig, err := GenerateTLSConfig("", "", nil, tlsNextProtos)
	if err != nil {
		return nil, err
	}
	listener, err := quic.ListenAddr(localAddress, tlsConfig, quicConfig)
	if err != nil {
		return nil, err
	}
	conn, err := listener.Accept(context.TODO())
	if err != nil {
		return nil, err
	}

	return conn, nil
}

func OpenClientConn(remoteAddress string, quicConfig *quic.Config, tlsNextProtos []string) (*quic.Conn, error) {
	return quic.DialAddr(context.TODO(), remoteAddress, &tls.Config{
		InsecureSkipVerify: true,
		NextProtos:         tlsNextProtos,
	}, quicConfig)
}
