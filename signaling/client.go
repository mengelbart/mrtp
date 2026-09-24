package signaling

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
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

// SendCandidates posts the candidates of session id, from the first one on,
// until they end or ctx is done.
func (c *Client) SendCandidates(ctx context.Context, id string, candidates *Candidates) error {
	for sent := 0; ; {
		list, ended, changed := candidates.since(sent)
		for _, candidate := range list {
			if err := c.addCandidate(ctx, id, candidate); err != nil {
				return err
			}
		}
		sent += len(list)
		if ended {
			return nil
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (c *Client) addCandidate(ctx context.Context, id string, candidate ICECandidate) error {
	body, err := json.Marshal(candidate)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.candidatesURL(id), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.do(req, http.StatusNoContent)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

// ReadCandidates passes each candidate the server sends for session id to
// handle, until the server has sent all of them or ctx is done.
func (c *Client) ReadCandidates(ctx context.Context, id string, handle func(ICECandidate) error) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.candidatesURL(id), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := c.do(req, http.StatusOK)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var event, data string
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if line != "" {
			field, value, _ := strings.Cut(line, ":")
			value = strings.TrimPrefix(value, " ")
			switch field {
			case "event":
				event = value
			case "data":
				data = value
			}
			continue
		}
		if event == endOfCandidates {
			return nil
		}
		var candidate ICECandidate
		if err = json.Unmarshal([]byte(data), &candidate); err != nil {
			return fmt.Errorf("failed to decode candidate: %w", err)
		}
		if err = handle(candidate); err != nil {
			return err
		}
		event, data = "", ""
	}
	if err = scanner.Err(); err != nil {
		return err
	}
	return io.ErrUnexpectedEOF
}

func (c *Client) candidatesURL(id string) string {
	return c.BaseURL + "/sessions/" + url.PathEscape(id) + "/candidates"
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
