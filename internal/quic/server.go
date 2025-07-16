package quic

import (
	"context"
	"crypto/tls"
	"log/slog"

	"github.com/quic-go/quic-go"
)

type Handler interface {
	Handle(quic.Connection)
}

type ServerOption func(*Server) error

type Server struct {
	addr       string
	tlsConfig  *tls.Config
	quicConfig *quic.Config
	handler    Handler
	ctx        context.Context
	cancelCtx  context.CancelFunc
	logger     *slog.Logger
}

func NewServer(opts ...ServerOption) (*Server, error) {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{
		addr:       ":8080",
		tlsConfig:  nil,
		quicConfig: &quic.Config{},
		handler:    nil,
		ctx:        ctx,
		cancelCtx:  cancel,
		logger:     slog.Default(),
	}
	for _, opt := range opts {
		if err := opt(s); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (s *Server) Listen() error {
	listener, err := quic.ListenAddr(s.addr, s.tlsConfig, s.quicConfig)
	if err != nil {
		return err
	}
	for {
		conn, err := listener.Accept(s.ctx)
		if err != nil {
			if err == context.Canceled {
				s.logger.Info("context canceled, listener exiting")
				return nil
			}
			s.logger.Error("failed to accept QUIC connection", "error", err)
			continue
		}
		// TODO: Manage goroutines here and wait for handlers to finish before
		// shutdown?
		go s.handler.Handle(conn)
	}
}
