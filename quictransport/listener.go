package quictransport

import (
	"context"
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
	tlsConfig, err := generateTLSConfig("", "", nil, tlsNextProtos)
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
