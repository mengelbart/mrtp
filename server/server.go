// Package server implements the server side of the signaling protocol in
// package signaling.
package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/mengelbart/mrtp/signaling"
)

// session is one media session opened through signaling.
type session interface {
	close() error
}

// Server manages the media sessions opened through signaling.
type Server struct {
	mediaHost string
	logger    *slog.Logger

	lock     sync.Mutex
	sessions map[string]session
}

// answerTimeout bounds gathering ICE candidates for a WebRTC answer.
const answerTimeout = 10 * time.Second

// New returns a Server that binds media sockets on mediaHost.
func New(mediaHost string) *Server {
	return &Server{
		mediaHost: mediaHost,
		logger:    slog.Default(),
		sessions:  map[string]session{},
	}
}

// Register adds the signaling routes to mux.
func (s *Server) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /sessions", s.handleOpen)
	mux.HandleFunc("DELETE /sessions/{id}", s.handleClose)
}

// Close closes all sessions.
func (s *Server) Close() error {
	s.lock.Lock()
	sessions := s.sessions
	s.sessions = map[string]session{}
	s.lock.Unlock()

	var errs []error
	for _, sess := range sessions {
		errs = append(errs, sess.close())
	}
	return errors.Join(errs...)
}

func (s *Server) handleOpen(w http.ResponseWriter, r *http.Request) {
	var request signaling.Request
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, fmt.Sprintf("invalid request: %v", err), http.StatusBadRequest)
		return
	}
	id, err := newID()
	if err != nil {
		http.Error(w, "failed to create session id", http.StatusInternalServerError)
		return
	}
	response := signaling.Response{ID: id}
	var sess session
	switch request.Protocol {
	case signaling.ProtocolRTPUDP:
		var udpSess *rtpUDPSession
		udpSess, err = newRTPUDPSession(id, s.mediaHost, s.logger)
		if err == nil {
			sess = udpSess
			response.RTP = &signaling.RTPEndpoint{Address: udpSess.src.LocalAddr().String()}
		}
	case signaling.ProtocolWebRTC:
		if request.WebRTC == nil {
			http.Error(w, "missing webrtc offer", http.StatusBadRequest)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), answerTimeout)
		var answer string
		sess, answer, err = newWebRTCSession(ctx, id, s.mediaHost, *request.WebRTC, s.logger)
		cancel()
		if err == nil {
			response.WebRTC = &signaling.WebRTCAnswer{SDP: answer}
		}
	default:
		http.Error(w, fmt.Sprintf("unsupported protocol %q", request.Protocol), http.StatusBadRequest)
		return
	}
	if errors.Is(err, errInvalidOffer) {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err != nil {
		s.logger.Error("failed to open session", "protocol", request.Protocol, "error", err)
		http.Error(w, "failed to open session", http.StatusInternalServerError)
		return
	}
	s.lock.Lock()
	s.sessions[id] = sess
	s.lock.Unlock()
	s.logger.Info("opened session", "id", id, "protocol", request.Protocol)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if err = json.NewEncoder(w).Encode(response); err != nil {
		s.logger.Error("failed to write session response", "id", id, "error", err)
	}
}

func (s *Server) handleClose(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.lock.Lock()
	sess, ok := s.sessions[id]
	delete(s.sessions, id)
	s.lock.Unlock()
	if !ok {
		http.Error(w, "unknown session", http.StatusNotFound)
		return
	}
	if err := sess.close(); err != nil {
		s.logger.Error("failed to close session", "id", id, "error", err)
	}
	w.WriteHeader(http.StatusNoContent)
}

func newID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
