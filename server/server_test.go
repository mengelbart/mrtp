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
	pionwebrtc "github.com/pion/webrtc/v4"
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
	resp, err := client.Open(ctx, rtpUDPRequest(signaling.DirectionSend, ""))
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

	sess := session[*rtpUDPSession](t, srv, resp.ID)
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

func TestRTPUDPSessionRecv(t *testing.T) {
	ts, _ := newTestServer(t)
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	client := &signaling.Client{BaseURL: ts.URL}
	resp, err := client.Open(context.Background(), rtpUDPRequest(signaling.DirectionRecv, conn.LocalAddr().String()))
	if err != nil {
		t.Fatal(err)
	}

	if err = conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 1500)
	n, from, err := conn.ReadFromUDP(buf)
	if err != nil {
		t.Fatal(err)
	}
	var packet rtp.Packet
	if err = packet.Unmarshal(buf[:n]); err != nil {
		t.Fatal(err)
	}
	if from.String() != resp.RTP.Address {
		t.Fatalf("received from %v, want %v", from, resp.RTP.Address)
	}
}

func TestRejectsRTPUDPRequest(t *testing.T) {
	ts, _ := newTestServer(t)
	client := &signaling.Client{BaseURL: ts.URL}
	for _, request := range []signaling.Request{
		{Protocol: signaling.ProtocolRTPUDP},
		rtpUDPRequest("sideways", ""),
		rtpUDPRequest(signaling.DirectionRecv, ""),
	} {
		_, err := client.Open(context.Background(), request)
		if err == nil || !strings.Contains(err.Error(), "400") {
			t.Errorf("opening %+v returned %v, want 400", request.RTP, err)
		}
	}
}

func TestWebRTCSessionRecv(t *testing.T) {
	ts, srv := newTestServer(t)
	client := newWebRTCReceiver(t)

	ctx := context.Background()
	signaler := &signaling.Client{BaseURL: ts.URL}
	resp := openWebRTC(t, ctx, signaler, client.webrtcClient)
	if !strings.Contains(resp.WebRTC.SDP, "a=sendonly") {
		t.Fatal("answer does not send")
	}
	waitFor(t, func() bool { return client.discard.Packets() > 0 })

	sess := session[*webrtcSession](t, srv, resp.ID)
	if err := signaler.Close(ctx, resp.ID); err != nil {
		t.Fatal(err)
	}
	if sess.packets() != 0 {
		t.Fatalf("server received %v packets, want 0", sess.packets())
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

	sess := session[*webrtcSession](t, srv, resp.ID)
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

func TestWebRTCTrickleSession(t *testing.T) {
	ts, srv := newTestServer(t)
	local := signaling.NewCandidates()
	client, track := newWebRTCClient(t, webrtc.OnICECandidate(func(c *pionwebrtc.ICECandidateInit) {
		local.Push((*signaling.ICECandidate)(c))
	}))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	offer, err := client.Offer(ctx)
	if err != nil {
		t.Fatal(err)
	}
	signaler := &signaling.Client{BaseURL: ts.URL}
	resp, err := signaler.Open(ctx, signaling.Request{
		Protocol: signaling.ProtocolWebRTC,
		WebRTC:   &signaling.WebRTCOffer{SDP: offer, Trickle: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = client.SetAnswer(resp.WebRTC.SDP); err != nil {
		t.Fatal(err)
	}
	sent := make(chan error, 1)
	go func() { sent <- signaler.SendCandidates(ctx, resp.ID, local) }()
	var received atomic.Int64
	err = signaler.ReadCandidates(ctx, resp.ID, func(c signaling.ICECandidate) error {
		received.Add(1)
		return client.AddICECandidate(pionwebrtc.ICECandidateInit(c))
	})
	if err != nil {
		t.Fatalf("reading candidates: %v", err)
	}
	if received.Load() == 0 {
		t.Fatal("server sent no candidates")
	}
	if err = <-sent; err != nil {
		t.Fatalf("sending candidates: %v", err)
	}
	select {
	case <-client.connected:
	case <-ctx.Done():
		t.Fatal("peer connection not established")
	}

	const n = 10
	sendRTP(t, track, n)
	sess := session[*webrtcSession](t, srv, resp.ID)
	waitFor(t, func() bool { return sess.packets() >= n })
}

func TestCandidatesOfNonTrickleSession(t *testing.T) {
	ts, _ := newTestServer(t)
	ctx := context.Background()
	signaler := &signaling.Client{BaseURL: ts.URL}
	resp, err := signaler.Open(ctx, rtpUDPRequest(signaling.DirectionSend, ""))
	if err != nil {
		t.Fatal(err)
	}
	err = signaler.ReadCandidates(ctx, resp.ID, func(signaling.ICECandidate) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "400") {
		t.Fatalf("reading candidates of an RTP session returned %v, want 400", err)
	}
}

func TestPreflight(t *testing.T) {
	ts, _ := newTestServer(t)
	for _, path := range []string{"/sessions", "/sessions/x", "/sessions/x/candidates"} {
		req, err := http.NewRequest(http.MethodOptions, ts.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			t.Errorf("OPTIONS %v returned %v, want 204", path, resp.Status)
		}
		if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "*" {
			t.Errorf("OPTIONS %v allows origin %q, want *", path, got)
		}
	}
}

// session returns the open session id as a T.
func session[T signaling.Session](t *testing.T, srv *Server, id string) T {
	t.Helper()
	sess, ok := srv.handler.Session(id)
	if !ok {
		t.Fatalf("no session %v", id)
	}
	return sess.(T)
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

// newWebRTCClient returns a client that sends on the returned track.
func newWebRTCClient(t *testing.T, opts ...webrtc.Option) (*webrtcClient, *webrtc.RTPSender) {
	t.Helper()
	client := newWebRTCTransport(t, opts...)
	track, err := client.AddLocalTrackWithCodec(mrtp.Fake.MimeType())
	if err != nil {
		t.Fatal(err)
	}
	return client, track
}

// webrtcReceiver is a test client that drops the RTP it receives.
type webrtcReceiver struct {
	*webrtcClient
	discard *pipeline.Discard[mrtp.RTPPacket]
}

// newWebRTCReceiver returns a client that offers one recvonly video m-line.
func newWebRTCReceiver(t *testing.T) *webrtcReceiver {
	t.Helper()
	r := &webrtcReceiver{discard: pipeline.NewDiscard[mrtp.RTPPacket]()}
	runner := pipeline.NewRunner()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = runner.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
		_ = runner.Close()
	})
	r.webrtcClient = newWebRTCTransport(t, webrtc.OnTrack(func(receiver *webrtc.RTPReceiver) {
		g := pipeline.NewGraph()
		if err := g.Connect(receiver, r.discard); err != nil {
			t.Error(err)
			return
		}
		runner.Add(g)
	}))
	if err := r.AddRemoteVideoTrack(); err != nil {
		t.Fatal(err)
	}
	return r
}

func newWebRTCTransport(t *testing.T, opts ...webrtc.Option) *webrtcClient {
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
	client.Transport, err = webrtc.NewTransport(options...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func rtpUDPRequest(direction, address string) signaling.Request {
	return signaling.Request{
		Protocol: signaling.ProtocolRTPUDP,
		RTP:      &signaling.RTPRequest{Direction: direction, Address: address},
	}
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
