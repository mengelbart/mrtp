package http

import (
	"net/http"

	"github.com/mengelbart/mrtp/internal/control"
)

type SessionStore interface {
	CreateSession(control.User, control.Session) (control.SessionID, error)
	GetSession(userID string, sessionID control.SessionID) (control.Session, error)
}

type CreatePeerConnectionResponse struct {
	PeerConnectionID string `json:"peer_connection_id"`
}

type GetPeerConnectionResponse struct {
	PeerConnectionID string `json:"peer_connection_id"`
}

func (a *API) CreatePeerConnection(w http.ResponseWriter, r *http.Request) {
	user := getUser(r)
	pc, err := control.NewPeerConnection()
	if err != nil {
		http.Error(w, "failed to create peer connection", http.StatusInternalServerError)
		return
	}

	sessionID, err := a.sessionStore.CreateSession(user, pc)
	if err != nil {
		http.Error(w, "failed to create session", http.StatusInternalServerError)
		return
	}
	pc.ID = sessionID

	writeJSONResponse(w, CreatePeerConnectionResponse{PeerConnectionID: string(sessionID)})
}

func (a *API) GetPeerConnection(w http.ResponseWriter, r *http.Request) {
	user := getUser(r)
	sessionID := r.PathValue("peer_connection")
	session, err := a.sessionStore.GetSession(user.ID, control.SessionID(sessionID))
	if err != nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	pc, ok := session.(*control.PeerConnection)
	if !ok {
		http.Error(w, "invalid session type", http.StatusBadRequest)
		return
	}

	writeJSONResponse(w, GetPeerConnectionResponse{PeerConnectionID: string(pc.ID)})
}

func (a *API) CreateSignaler(w http.ResponseWriter, r *http.Request) {
	user := getUser(r)
	sessionID := r.PathValue("peer_connection")
	session, err := a.sessionStore.GetSession(user.ID, control.SessionID(sessionID))
	if err != nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	pc, ok := session.(*control.PeerConnection)
	if !ok {
		http.Error(w, "invalid session type", http.StatusBadRequest)
		return
	}

	conn, err := a.upgrader.Upgrade(w, r, nil)
	if err != nil {
		http.Error(w, "failed to upgrade to websocket", http.StatusInternalServerError)
		return
	}
	wsConn := newWSConn(conn, pc)
	pc.SetSignaler(wsConn)
}
