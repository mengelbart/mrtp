package http

import (
	"log/slog"
	"net/http"

	"github.com/julienschmidt/httprouter"
	"github.com/mengelbart/mrtp/internal/model"
)

type ChannelService interface {
	CreateChannel() (ID int)
	ListChannels() ([]*model.Channel, error)
}

type API struct {
	logger   *slog.Logger
	channels ChannelService
}

func NewApi() *API {
	return &API{
		logger: slog.Default(),
	}
}

func (a *API) RegisterRoutes(mux *httprouter.Router) {
	mux.HandlerFunc("POST", "/api/v1/channels", a.CreateChannel)
	mux.HandlerFunc("GET", "/api/v1/channels", a.ListChannels)
	mux.HandlerFunc("GET", "/api/v1/channels/:id", a.ListChannels)
}

func (a *API) GetChannel(w http.ResponseWriter, r *http.Request) {
}

func (a *API) ListChannels(w http.ResponseWriter, r *http.Request) {
}

func (a *API) CreateChannel(w http.ResponseWriter, r *http.Request) {
	a.channels.CreateChannel()
}

func (a *API) UpdateChannel(w http.ResponseWriter, r *http.Request) {
}

func (a *API) DeleteChannel(w http.ResponseWriter, r *http.Request) {
}

func (a *API) CreatePublisher(w http.ResponseWriter, r *http.Request) {

}

func (a *API) CreateStream(w http.ResponseWriter, r *http.Request) {

}
