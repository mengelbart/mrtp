package control

import (
	"fmt"
	"sync"
	"time"
)

type SessionID string

type Session interface {
}

type sessionRegistryKey struct {
	userID    string
	sessionID SessionID
}

type SessionRegistry struct {
	lock sync.Mutex

	sessions map[sessionRegistryKey]Session
}

func NewSessionRegistry() *SessionRegistry {
	return &SessionRegistry{
		sessions: make(map[sessionRegistryKey]Session),
	}
}

func (r *SessionRegistry) CreateSession(user User, session Session) (SessionID, error) {
	r.lock.Lock()
	defer r.lock.Unlock()

	id := SessionID(fmt.Sprintf("session-%d", time.Now().Unix())) // TODO: generate unique session ID
	r.sessions[sessionRegistryKey{userID: user.ID, sessionID: id}] = session
	return id, nil
}

func (r *SessionRegistry) GetSession(userID string, sessionID SessionID) (Session, error) {
	r.lock.Lock()
	defer r.lock.Unlock()

	session, ok := r.sessions[sessionRegistryKey{userID: userID, sessionID: sessionID}]
	if !ok {
		return nil, fmt.Errorf("session not found")
	}
	return session, nil
}
