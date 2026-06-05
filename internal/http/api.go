package http

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/gorilla/websocket"
	"github.com/mengelbart/mrtp/internal/control"
)

type ctxKey string

type APIOption func(*API) error

func WithAuthenticator(auth Authenticator) APIOption {
	return func(api *API) error {
		api.authenticator = auth
		return nil
	}
}

type API struct {
	authenticator Authenticator
	tokenManager  TokenManager
	userStore     UserStore
	sessionStore  SessionStore

	upgrader *websocket.Upgrader
}

func NewApi(options ...APIOption) (*API, error) {
	api := &API{
		authenticator: NewStaticAuthenticator(map[string]string{"admin": "password"}),
		tokenManager:  NewJWTTokenManager(nil),
		userStore:     control.NewMemoryUserStore(map[string]*control.User{"admin": {ID: "admin"}}),
		sessionStore:  control.NewSessionRegistry(),
		upgrader: &websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			CheckOrigin: func(r *http.Request) bool {
				return true // TODO
			},
		},
	}
	for _, option := range options {
		if err := option(api); err != nil {
			return nil, err
		}
	}
	return api, nil
}

func (a *API) Health(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte("OK")); err != nil {
		slog.Error("failed to write health response", "error", err)
		return
	}
}

func writeJSONResponse(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(data); err != nil {
		slog.Error("failed to encode response", "error", err)
	}
}
