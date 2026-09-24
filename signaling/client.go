package signaling

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// Client opens and closes sessions on a signaling server.
type Client struct {
	// BaseURL is the server's URL, e.g. http://127.0.0.1:8080.
	BaseURL string
	// HTTP is the client used for requests, http.DefaultClient if nil.
	HTTP *http.Client
}

// Open requests a new session.
func (c *Client) Open(ctx context.Context, request Request) (Response, error) {
	body, err := json.Marshal(request)
	if err != nil {
		return Response{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/sessions", bytes.NewReader(body))
	if err != nil {
		return Response{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.do(req, http.StatusCreated)
	if err != nil {
		return Response{}, err
	}
	defer resp.Body.Close()

	var response Response
	if err = json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return Response{}, fmt.Errorf("failed to decode session response: %w", err)
	}
	return response, nil
}

// Close closes the session with the given id.
func (c *Client) Close(ctx context.Context, id string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.BaseURL+"/sessions/"+url.PathEscape(id), nil)
	if err != nil {
		return err
	}
	resp, err := c.do(req, http.StatusNoContent)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

func (c *Client) do(req *http.Request, want int) (*http.Response, error) {
	client := c.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != want {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		_ = resp.Body.Close()
		return nil, fmt.Errorf("%v %v: %v: %s", req.Method, req.URL, resp.Status, bytes.TrimSpace(msg))
	}
	return resp, nil
}
