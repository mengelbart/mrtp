package http

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	"golang.org/x/sync/errgroup"
)

type Option func(*Server) error

func H1Address(address string) Option {
	return func(s *Server) error {
		s.h1.Addr = address
		return nil
	}
}

func H2Address(address string) Option {
	return func(s *Server) error {
		s.h2.Addr = address
		return nil
	}
}

func H3Address(address string) Option {
	return func(s *Server) error {
		s.h3.Addr = address
		return nil
	}
}

func Handle(handler http.Handler) Option {
	return func(s *Server) error {
		s.handler = handler
		return nil
	}
}

func RequestLogger(logger *slog.Logger) Option {
	return func(s *Server) error {
		s.requestLogger = logger
		return nil
	}
}

func Certificate(cert tls.Certificate) Option {
	return func(s *Server) error {
		s.tlsConfig.Certificates = []tls.Certificate{cert}
		return nil
	}
}

func CertificateFile(file string) Option {
	return func(s *Server) error {
		s.certFile = file
		return nil
	}
}

func CertificateKeyFile(file string) Option {
	return func(s *Server) error {
		s.keyFile = file
		return nil
	}
}

type Server struct {
	certFile string
	keyFile  string

	logger        *slog.Logger
	requestLogger *slog.Logger

	handler http.Handler

	tlsConfig  *tls.Config
	quicConfig *quic.Config
	h1         *http.Server
	h2         *http.Server
	h3         *http3.Server
}

func NewServer(opts ...Option) (*Server, error) {
	s := &Server{
		certFile:      "",
		keyFile:       "",
		logger:        slog.Default(),
		requestLogger: nil,
		handler:       http.DefaultServeMux,
		tlsConfig:     &tls.Config{NextProtos: []string{http3.NextProtoH3}},
		quicConfig:    &quic.Config{EnableDatagrams: true},
		h1:            &http.Server{},
		h2:            &http.Server{},
		h3:            &http3.Server{},
	}
	for _, opt := range opts {
		if err := opt(s); err != nil {
			return nil, err
		}
	}
	if s.tlsConfig.Certificates == nil {
		cert, err := tls.LoadX509KeyPair(s.certFile, s.keyFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read TLS certificate or key: %v", err)
		}
		s.tlsConfig.Certificates = []tls.Certificate{cert}
	}
	s.h2.TLSConfig = s.tlsConfig
	s.h3.TLSConfig = s.tlsConfig

	_, port, err := net.SplitHostPort(s.h2.Addr)
	if err != nil {
		return nil, err
	}
	s.h1.Handler = s.setAltSvcHeader(s.redirectHTTP(port))

	s.handler = s.setAltSvcHeader(s.handler)
	if s.requestLogger != nil {
		s.handler = s.logRequest(s.handler)
	}
	s.h2.Handler = s.handler
	s.h3.Handler = s.handler

	return s, nil
}

func (s *Server) ListenAndServe() error {
	eg, ctx := errgroup.WithContext(context.Background())
	eg.Go(func() error {
		s.logger.Info("serving HTTP/1.1", "address", s.h1.Addr)
		return s.h1.ListenAndServe()
	})
	eg.Go(func() error {
		s.logger.Info("serving HTTP/2", "address", s.h2.Addr)
		return s.h2.ListenAndServeTLS("", "")
	})
	eg.Go(func() error {
		s.logger.Info("serving HTTP/3", "address", s.h3.Addr)
		return s.ListenAndServeQUIC(ctx)
	})
	eg.Go(func() error {
		<-ctx.Done()
		err := context.Cause(ctx)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if errors.Is(err, http.ErrServerClosed) {
			h2Err := s.h2.Shutdown(ctx)
			if err != nil {
				err = errors.Join(err, h2Err)
			}
			h3Err := s.h3.Shutdown(ctx)
			if err != nil {
				err = errors.Join(err, h3Err)
			}
			return err
		}
		h2Err := s.h2.Close()
		if err != nil {
			err = errors.Join(err, h2Err)
		}
		h3Err := s.h3.Close()
		if err != nil {
			err = errors.Join(err, h3Err)
		}
		return err
	})
	return eg.Wait()
}

func (s *Server) ListenAndServeQUIC(ctx context.Context) error {
	addr, err := net.ResolveUDPAddr("udp", s.h3.Addr)
	if err != nil {
		return err
	}
	udpConn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return err
	}
	tr := quic.Transport{
		Conn: udpConn,
	}
	ln, err := tr.Listen(s.tlsConfig, s.quicConfig)
	if err != nil {
		return err
	}
	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		conn, err := ln.Accept(ctx)
		if errors.Is(err, quic.ErrServerClosed) || ctx.Err() != nil {
			return http.ErrServerClosed
		}
		if err != nil {
			return err
		}
		switch conn.ConnectionState().TLS.NegotiatedProtocol {
		case http3.NextProtoH3:
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := s.h3.ServeQUICConn(conn); err != nil {
					s.logger.Error("error on serving QUICConn", "error", err)
				}
				if err := conn.CloseWithError(0, "bye"); err != nil {
					s.logger.Error("error on closing QUIC conn", "error", err)
				}
			}()
		}
	}
}

// Middleware

func (s *Server) setAltSvcHeader(next http.Handler) http.Handler {
	_, portStr, err := net.SplitHostPort(s.h3.Addr)
	if err != nil {
		s.logger.Error("failed to set Alt-Svc header", "error", err)
		return next
	}
	portInt, err := net.LookupPort("tcp", portStr)
	if err != nil {
		s.logger.Error("failed to set Alt-Svc header", "error", err)
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor < 3 {
			altSvc := fmt.Sprintf(`%s=":%d"; ma=2592000`, http3.NextProtoH3, portInt)
			w.Header()["Alt-Svc"] = []string{altSvc}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) logRequest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.requestLogger.Info("got request", "request", r)
		next.ServeHTTP(w, r)
	})
}

func (s *Server) redirectHTTP(port string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			slog.Error("failed to redirect HTTP request to HTTPS server", "error", err)
			http.Error(w, "invalid host/port", http.StatusBadRequest)
			return
		}
		u := r.URL
		u.Host = net.JoinHostPort(host, port)
		u.Scheme = "https"
		http.Redirect(w, r, u.String(), http.StatusMovedPermanently)
	})
}
