package server

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mengelbart/mrtp"
	"github.com/mengelbart/mrtp/pipeline"
	"github.com/mengelbart/mrtp/signaling"
	"github.com/mengelbart/mrtp/webrtc"
	"github.com/pion/rtp"
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
	sess := srv.sessions[resp.ID].(*rtpUDPSession)
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

func TestWebRTCSession(t *testing.T) {
	ts, srv := newTestServer(t)
	client, track := newWebRTCClient(t)

	ctx := context.Background()
	signaler := &signaling.Client{BaseURL: ts.URL}
	resp := openWebRTC(t, ctx, signaler, client)

	const n = 10
	sendRTP(t, track, n)

	srv.lock.Lock()
	sess := srv.sessions[resp.ID].(*webrtcSession)
	srv.lock.Unlock()
	waitFor(t, func() bool { return sess.packets() >= n })

	if err := signaler.Close(ctx, resp.ID); err != nil {
		t.Fatal(err)
	}
	if err := signaler.Close(ctx, resp.ID); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("closing twice returned %v, want 404", err)
	}
}

func TestWebRTCFeedback(t *testing.T) {
	ts, _ := newTestServer(t)
	bwe := &countingBWE{}
	client, track := newWebRTCClient(t, webrtc.EnableCCFBReceiver(), webrtc.SetBWE(bwe))
	rtcp := track.RTCPReceiver()
	defer rtcp.Close()

	signaler := &signaling.Client{BaseURL: ts.URL}
	resp := openWebRTC(t, context.Background(), signaler, client)
	if !strings.Contains(resp.WebRTC.SDP, "ack ccfb") {
		t.Fatal("answer does not negotiate CCFB")
	}
	if strings.Contains(resp.WebRTC.SDP, "transport-cc") {
		t.Fatal("answer negotiates TWCC")
	}

	deadline := time.Now().Add(5 * time.Second)
	for seq := uint16(1); bwe.acks.Load() == 0; seq++ {
		if time.Now().After(deadline) {
			t.Fatal("no CCFB acks received")
		}
		writeRTP(t, track, seq)
		time.Sleep(5 * time.Millisecond)
	}
}

func TestRejectsWebRTCWithoutOffer(t *testing.T) {
	ts, _ := newTestServer(t)
	client := &signaling.Client{BaseURL: ts.URL}
	_, err := client.Open(context.Background(), signaling.Request{Protocol: signaling.ProtocolWebRTC})
	if err == nil || !strings.Contains(err.Error(), "400") {
		t.Fatalf("opening webrtc without offer returned %v, want 400", err)
	}
}

func newTestServer(t *testing.T) (*httptest.Server, *Server) {
	t.Helper()
	srv := New("127.0.0.1")
	t.Cleanup(func() { _ = srv.Close() })
	mux := http.NewServeMux()
	srv.Register(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, srv
}

// webrtcClient is the offering side of a test session.
type webrtcClient struct {
	*webrtc.Transport
	connected chan struct{}
}

func newWebRTCClient(t *testing.T, opts ...webrtc.Option) (*webrtcClient, *webrtc.RTPSender) {
	t.Helper()
	client := &webrtcClient{connected: make(chan struct{})}
	var once sync.Once
	options := append([]webrtc.Option{
		webrtc.OnConnected(func() { once.Do(func() { close(client.connected) }) }),
		webrtc.RegisterDefaultCodecs(),
		webrtc.RegisterFakeCodec(),
		webrtc.SetICEServers(nil),
		webrtc.IncludeLoopbackCandidates(),
		webrtc.ListenIP(net.ParseIP("127.0.0.1")),
	}, opts...)
	var err error
	client.Transport, err = webrtc.NewTransport(nil, true, options...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	track, err := client.AddLocalTrackWithCodec(mrtp.Fake.MimeType())
	if err != nil {
		t.Fatal(err)
	}
	return client, track
}

// openWebRTC negotiates a session and waits until the peer connection is up.
func openWebRTC(t *testing.T, ctx context.Context, signaler *signaling.Client, client *webrtcClient) signaling.Response {
	t.Helper()
	offer, err := client.Offer(ctx)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := signaler.Open(ctx, signaling.Request{
		Protocol: signaling.ProtocolWebRTC,
		WebRTC:   &signaling.WebRTCOffer{SDP: offer},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.WebRTC == nil {
		t.Fatal("response has no webrtc answer")
	}
	if err = client.SetAnswer(resp.WebRTC.SDP); err != nil {
		t.Fatal(err)
	}
	select {
	case <-client.connected:
	case <-time.After(5 * time.Second):
		t.Fatal("peer connection not established")
	}
	return resp
}

var rtpPool = pipeline.NewPool(func() *mrtp.RTPPacket { return &mrtp.RTPPacket{} }, func(*mrtp.RTPPacket) {})

func writeRTP(t *testing.T, track *webrtc.RTPSender, seq uint16) {
	t.Helper()
	data, err := (&rtp.Packet{
		Header:  rtp.Header{Version: 2, SequenceNumber: seq, Timestamp: uint32(seq), SSRC: 1},
		Payload: make([]byte, 100),
	}).Marshal()
	if err != nil {
		t.Fatal(err)
	}
	p := rtpPool.Get()
	p.Value().Data = data
	if err = track.Write(p); err != nil {
		t.Fatal(err)
	}
}

// sendRTP writes n packets.
func sendRTP(t *testing.T, track *webrtc.RTPSender, n int) {
	t.Helper()
	for seq := range uint16(n) {
		writeRTP(t, track, seq+1)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

type countingBWE struct {
	acks atomic.Uint64
}

func (b *countingBWE) OnAck(uint64, int, time.Time, time.Time, mrtp.ECN) { b.acks.Add(1) }
func (b *countingBWE) OnLoss(uint64, int, time.Time)                     {}
func (b *countingBWE) UpdateRTT(time.Duration)                           {}
func (b *countingBWE) UpdateECNCounts(uint64, uint64, uint64)            {}
func (b *countingBWE) UpdateTargetRate(time.Time) int                    { return 0 }
