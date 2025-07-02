package webrtc

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/pion/webrtc/v4"
)

type SignalHandler interface {
	HandleSessionDescription(*webrtc.SessionDescription) error
	HandleICECandidate(webrtc.ICECandidateInit) error
}

type HTTPSignalingHandler struct {
	sh SignalHandler
}

func NewHTTPSignalingHandler(sh SignalHandler) *HTTPSignalingHandler {
	return &HTTPSignalingHandler{
		sh: sh,
	}
}

func (h *HTTPSignalingHandler) HandleSessionDescription(w http.ResponseWriter, r *http.Request) {
	sdp := webrtc.SessionDescription{}
	if err := json.NewDecoder(r.Body).Decode(&sdp); err != nil {
		http.Error(w, "failed to decode session description", http.StatusBadRequest)
		return
	}
	if err := h.sh.HandleSessionDescription(&sdp); err != nil {
		http.Error(w, fmt.Sprintf("failed to handle session description: %v", err.Error()), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (h *HTTPSignalingHandler) HandleCandidate(w http.ResponseWriter, r *http.Request) {
	candidate, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to read candidate: %v", err.Error()), http.StatusBadRequest)
		return
	}
	if err = h.sh.HandleICECandidate(webrtc.ICECandidateInit{Candidate: string(candidate)}); err != nil {
		http.Error(w, fmt.Sprintf("failed to handle ICE candidate: %v", err.Error()), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusOK)
}
