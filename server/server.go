// Package server opens the media sessions clients request through the
// signaling protocol in package signaling.
package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/mengelbart/mrtp/signaling"
)

// Server opens the media sessions requested through signaling.
type Server struct {
	mediaHost string
	logger    *slog.Logger
	handler   *signaling.Handler
}

// answerTimeout bounds gathering ICE candidates for a WebRTC answer.
const answerTimeout = 10 * time.Second

// New returns a Server that binds media sockets on mediaHost.
func New(mediaHost string) *Server {
	s := &Server{
		mediaHost: mediaHost,
		logger:    slog.Default(),
	}
	s.handler = signaling.NewHandler(s)
	return s
}

// Register adds the signaling routes to mux.
func (s *Server) Register(mux *http.ServeMux) {
	s.handler.Register(mux)
}

// Close closes all sessions.
func (s *Server) Close() error {
	return s.handler.Close()
}

// Accept implements signaling.Acceptor.
func (s *Server) Accept(ctx context.Context, id string, request signaling.Request) (signaling.Session, signaling.Response, error) {
	switch request.Protocol {
	case signaling.ProtocolRTPUDP:
		sess, err := newRTPUDPSession(id, s.mediaHost, s.logger)
		if err != nil {
			return nil, signaling.Response{}, err
		}
		return sess, signaling.Response{RTP: &signaling.RTPEndpoint{Address: sess.src.LocalAddr().String()}}, nil
	case signaling.ProtocolWebRTC:
		if request.WebRTC == nil {
			return nil, signaling.Response{}, fmt.Errorf("%w: missing webrtc offer", signaling.ErrBadRequest)
		}
		ctx, cancel := context.WithTimeout(ctx, answerTimeout)
		defer cancel()
		sess, answer, err := newWebRTCSession(ctx, id, s.mediaHost, *request.WebRTC, s.logger)
		if err != nil {
			return nil, signaling.Response{}, err
		}
		return sess, signaling.Response{WebRTC: &signaling.WebRTCAnswer{SDP: answer}}, nil
	default:
		return nil, signaling.Response{}, fmt.Errorf("%w: unsupported protocol %q", signaling.ErrBadRequest, request.Protocol)
	}
}
