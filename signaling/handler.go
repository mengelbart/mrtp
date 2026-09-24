package signaling

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
)

var (
	ErrBadRequest = errors.New("bad request")
	ErrConflict   = errors.New("conflict")
)

// Session is a media session opened through a Handler.
type Session interface {
	Close() error
}

// TrickleSession is a Session that can exchange ICE candidates after it is
// opened.
type TrickleSession interface {
	Session
	AddICECandidate(ICECandidate) error
	// LocalCandidates returns the session's own candidates, or nil if the
	// session does not trickle.
	LocalCandidates() *Candidates
}

type Acceptor interface {
	// Accept opens session id for request. The Handler sets the ID of the
	// returned response.
	Accept(ctx context.Context, id string, request Request) (Session, Response, error)
}

// Handler serves the signaling protocol and keeps the sessions its Acceptor
// opens. It allows cross-origin requests from any origin, so that a browser
// page can signal to it.
type Handler struct {
	acceptor Acceptor
	logger   *slog.Logger

	lock     sync.Mutex
	sessions map[string]*entry
}

type entry struct {
	session Session
	// closed ends the session's candidate streams.
	closed chan struct{}
}

func NewHandler(acceptor Acceptor) *Handler {
	return &Handler{
		acceptor: acceptor,
		logger:   slog.Default(),
		sessions: map[string]*entry{},
	}
}

// Register adds the signaling routes to mux.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /sessions", cors(h.handleOpen))
	mux.HandleFunc("DELETE /sessions/{id}", cors(h.handleClose))
	mux.HandleFunc("POST /sessions/{id}/candidates", cors(h.handleAddCandidate))
	mux.HandleFunc("GET /sessions/{id}/candidates", cors(h.handleCandidates))
	mux.HandleFunc("OPTIONS /sessions", cors(preflight))
	mux.HandleFunc("OPTIONS /sessions/{id}", cors(preflight))
	mux.HandleFunc("OPTIONS /sessions/{id}/candidates", cors(preflight))
}

// Session returns the open session with the given id.
func (h *Handler) Session(id string) (Session, bool) {
	h.lock.Lock()
	defer h.lock.Unlock()
	e, ok := h.sessions[id]
	if !ok {
		return nil, false
	}
	return e.session, true
}

// Close closes all sessions.
func (h *Handler) Close() error {
	h.lock.Lock()
	sessions := h.sessions
	h.sessions = map[string]*entry{}
	h.lock.Unlock()

	var errs []error
	for _, e := range sessions {
		close(e.closed)
		errs = append(errs, e.session.Close())
	}
	return errors.Join(errs...)
}

func (h *Handler) handleOpen(w http.ResponseWriter, r *http.Request) {
	var request Request
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, fmt.Sprintf("invalid request: %v", err), http.StatusBadRequest)
		return
	}
	id, err := newID()
	if err != nil {
		http.Error(w, "failed to create session id", http.StatusInternalServerError)
		return
	}
	session, response, err := h.acceptor.Accept(r.Context(), id, request)
	switch {
	case errors.Is(err, ErrBadRequest):
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	case errors.Is(err, ErrConflict):
		http.Error(w, err.Error(), http.StatusConflict)
		return
	case err != nil:
		h.logger.Error("failed to open session", "protocol", request.Protocol, "error", err)
		http.Error(w, "failed to open session", http.StatusInternalServerError)
		return
	}
	h.lock.Lock()
	h.sessions[id] = &entry{session: session, closed: make(chan struct{})}
	h.lock.Unlock()
	h.logger.Info("opened session", "id", id, "protocol", request.Protocol)

	response.ID = id
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if err = json.NewEncoder(w).Encode(response); err != nil {
		h.logger.Error("failed to write session response", "id", id, "error", err)
	}
}

func (h *Handler) handleClose(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	h.lock.Lock()
	e, ok := h.sessions[id]
	delete(h.sessions, id)
	h.lock.Unlock()
	if !ok {
		http.Error(w, "unknown session", http.StatusNotFound)
		return
	}
	close(e.closed)
	if err := e.session.Close(); err != nil {
		h.logger.Error("failed to close session", "id", id, "error", err)
	}
	w.WriteHeader(http.StatusNoContent)
}

// trickleSession looks up the session a candidates request names, and writes
// the error response if it does not trickle.
func (h *Handler) trickleSession(w http.ResponseWriter, r *http.Request) (TrickleSession, *entry, bool) {
	h.lock.Lock()
	e, ok := h.sessions[r.PathValue("id")]
	h.lock.Unlock()
	if !ok {
		http.Error(w, "unknown session", http.StatusNotFound)
		return nil, nil, false
	}
	session, ok := e.session.(TrickleSession)
	if !ok || session.LocalCandidates() == nil {
		http.Error(w, "session does not trickle ICE candidates", http.StatusBadRequest)
		return nil, nil, false
	}
	return session, e, true
}

func (h *Handler) handleAddCandidate(w http.ResponseWriter, r *http.Request) {
	session, _, ok := h.trickleSession(w, r)
	if !ok {
		return
	}
	var candidate ICECandidate
	if err := json.NewDecoder(r.Body).Decode(&candidate); err != nil {
		http.Error(w, fmt.Sprintf("invalid candidate: %v", err), http.StatusBadRequest)
		return
	}
	if err := session.AddICECandidate(candidate); err != nil {
		http.Error(w, fmt.Sprintf("failed to add candidate: %v", err), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleCandidates streams the session's candidates as server-sent events,
// from the first one on, until they end or the session closes.
func (h *Handler) handleCandidates(w http.ResponseWriter, r *http.Request) {
	session, e, ok := h.trickleSession(w, r)
	if !ok {
		return
	}
	candidates := session.LocalCandidates()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	rc := http.NewResponseController(w)
	for sent := 0; ; {
		list, ended, changed := candidates.since(sent)
		for _, candidate := range list {
			data, err := json.Marshal(candidate)
			if err != nil {
				h.logger.Error("failed to encode candidate", "error", err)
				return
			}
			if _, err = fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
				return
			}
		}
		sent += len(list)
		if ended {
			_, _ = fmt.Fprintf(w, "event: %s\ndata: null\n\n", endOfCandidates)
			_ = rc.Flush()
			return
		}
		if err := rc.Flush(); err != nil {
			return
		}
		select {
		case <-changed:
		case <-e.closed:
			return
		case <-r.Context().Done():
			return
		}
	}
}

// endOfCandidates is the event that ends a candidate stream.
const endOfCandidates = "end-of-candidates"

func cors(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		next(w, r)
	}
}

func preflight(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}

func newID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
