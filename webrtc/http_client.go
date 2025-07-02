package webrtc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/pion/webrtc/v4"
)

type HTTPClientSignaler struct {
	baseURL string
}

func NewHTTPClientSignaler(baseURL string) *HTTPClientSignaler {
	return &HTTPClientSignaler{
		baseURL: baseURL,
	}
}

// SendICECandidate implements Signaler.
func (h *HTTPClientSignaler) SendICECandidate(candidate *webrtc.ICECandidate) error {
	payload := []byte(candidate.ToJSON().Candidate)
	return h.post("candidate", payload)
}

// SendSessionDescription implements Signaler.
func (h *HTTPClientSignaler) SendSessionDescription(sd *webrtc.SessionDescription) error {
	payload, err := json.Marshal(sd)
	if err != nil {
		return err
	}
	return h.post("session_description", payload)
}

func (h *HTTPClientSignaler) post(endpoint string, payload []byte) error {
	resp, err := http.Post(fmt.Sprintf("%v/%v", h.baseURL, endpoint), "application/json; charset=utf-8", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("failed to post session description: %v", resp.StatusCode)
	}
	return nil
}
