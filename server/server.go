// Package server opens the media sessions clients request through the
// signaling protocol in package signaling.
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/mengelbart/mrtp"
	"github.com/mengelbart/mrtp/signaling"
)

// Config configures a Server.
type Config struct {
	// MediaHost is the IP media sockets bind to and WebRTC ICE candidates are
	// restricted to.
	MediaHost string
	// SourceDir holds the IVF files clients may request. Empty allows none.
	SourceDir string
	// SinkDir is where received VP8 and VP9 tracks are recorded as IVF
	// files named after the session. Empty drops them.
	SinkDir string
}

// Server opens the media sessions requested through signaling.
type Server struct {
	mediaHost string
	media     *media
	logger    *slog.Logger
	handler   *signaling.Handler
}

// answerTimeout bounds gathering ICE candidates for a WebRTC answer.
const answerTimeout = 10 * time.Second

// New returns a Server configured by config.
func New(config Config) (*Server, error) {
	m, err := openMedia(config.SourceDir, config.SinkDir)
	if err != nil {
		return nil, err
	}
	s := &Server{
		mediaHost: config.MediaHost,
		media:     m,
		logger:    slog.Default(),
	}
	s.handler = signaling.NewHandler(s)
	return s, nil
}

// Register adds the signaling routes to mux.
func (s *Server) Register(mux *http.ServeMux) {
	s.handler.Register(mux)
}

// Close closes all sessions.
func (s *Server) Close() error {
	return errors.Join(s.handler.Close(), s.media.Close())
}

// Accept implements signaling.Acceptor.
func (s *Server) Accept(ctx context.Context, id string, request signaling.Request) (signaling.Session, signaling.Response, error) {
	switch request.Protocol {
	case signaling.ProtocolRTPUDP:
		if request.RTP == nil {
			return nil, signaling.Response{}, fmt.Errorf("%w: missing rtp request", signaling.ErrBadRequest)
		}
		sess, err := newRTPUDPSession(id, s.mediaHost, *request.RTP, request.Source, s.media, s.logger)
		if err != nil {
			return nil, signaling.Response{}, err
		}
		endpoint := &signaling.RTPEndpoint{Address: sess.addr.String()}
		if sess.sending {
			endpoint.Codec = sess.codec.String()
			endpoint.PayloadType = mrtp.DefaultPayloadType
		}
		return sess, signaling.Response{RTP: endpoint}, nil
	case signaling.ProtocolWebRTC:
		if request.WebRTC == nil {
			return nil, signaling.Response{}, fmt.Errorf("%w: missing webrtc offer", signaling.ErrBadRequest)
		}
		ctx, cancel := context.WithTimeout(ctx, answerTimeout)
		defer cancel()
		sess, answer, err := newWebRTCSession(ctx, id, s.mediaHost, *request.WebRTC, request.Source, s.media, s.logger)
		if err != nil {
			return nil, signaling.Response{}, err
		}
		return sess, signaling.Response{WebRTC: &signaling.WebRTCAnswer{SDP: answer}}, nil
	default:
		return nil, signaling.Response{}, fmt.Errorf("%w: unsupported protocol %q", signaling.ErrBadRequest, request.Protocol)
	}
}
