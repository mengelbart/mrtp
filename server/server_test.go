package server

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mengelbart/mrtp/signaling"
)

func TestRTPUDPSession(t *testing.T) {
	srv := New("127.0.0.1")
	defer srv.Close()
	mux := http.NewServeMux()
	srv.Register(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	ctx := context.Background()
	client := &signaling.Client{BaseURL: ts.URL}
	resp, err := client.Open(ctx, signaling.Request{Protocol: signaling.ProtocolRTPUDP})
	if err != nil {
		t.Fatal(err)
	}

	conn, err := net.Dial("udp", resp.RTP.Address)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	const n = 10
	for range n {
		if _, err = conn.Write([]byte{0x80, 96, 0, 1}); err != nil {
			t.Fatal(err)
		}
	}

	srv.lock.Lock()
	sess := srv.sessions[resp.ID]
	srv.lock.Unlock()
	deadline := time.Now().Add(time.Second)
	for sess.discard.Packets() < n {
		if time.Now().After(deadline) {
			t.Fatalf("received %v packets, want %v", sess.discard.Packets(), n)
		}
		time.Sleep(5 * time.Millisecond)
	}

	if err = client.Close(ctx, resp.ID); err != nil {
		t.Fatal(err)
	}
	if err = client.Close(ctx, resp.ID); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("closing twice returned %v, want 404", err)
	}
}

func TestRejectsUnknownProtocol(t *testing.T) {
	srv := New("127.0.0.1")
	defer srv.Close()
	mux := http.NewServeMux()
	srv.Register(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	client := &signaling.Client{BaseURL: ts.URL}
	_, err := client.Open(context.Background(), signaling.Request{Protocol: "carrier-pigeon"})
	if err == nil || !strings.Contains(err.Error(), "400") {
		t.Fatalf("opening an unknown protocol returned %v, want 400", err)
	}
}
