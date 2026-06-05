package http

import (
	"net/http"
)

func NewRouter(api *API) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /api/v1/login", api.Login)
	mux.HandleFunc("GET /api/v1/health", api.Auth(api.Health))

	mux.HandleFunc("POST /api/v1/peer-connections", api.Auth(api.User(api.CreatePeerConnection)))
	mux.HandleFunc("GET /api/v1/peer-connections/{peer_connection}", api.Auth(api.User(api.GetPeerConnection)))
	mux.HandleFunc("GET /api/v1/peer-connections/{peer_connection}/signaler", api.Auth(api.User(api.CreateSignaler)))

	return mux
}
