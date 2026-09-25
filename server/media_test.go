package server

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mengelbart/mrtp"
	"github.com/mengelbart/mrtp/element/mediafile"
	"github.com/mengelbart/mrtp/pipeline"
	"github.com/mengelbart/mrtp/signaling"
	"github.com/pion/rtp"
)

// clipFrames and clipInterval describe the clip writeClip writes.
const (
	clipFrames   = 50
	clipInterval = 10 * time.Millisecond
)

// writeClip writes a VP8 IVF file of clipFrames frames to dir/name.
func writeClip(t *testing.T, dir, name string) {
	t.Helper()
	file, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	sink := mediafile.NewIVFSink(file)
	if err = sink.Negotiate(mrtp.EncodedVideo{Codec: mrtp.VP8, Width: 64, Height: 48}); err != nil {
		t.Fatal(err)
	}
	pool := pipeline.NewPool(func() *mrtp.EncodedFrame { return &mrtp.EncodedFrame{} }, func(*mrtp.EncodedFrame) {})
	for i := range clipFrames {
		p := pool.Get()
		p.Value().Data = make([]byte, 100)
		p.Value().PTS = time.Duration(i) * clipInterval
		if err = sink.Write(p); err != nil {
			t.Fatal(err)
		}
	}
	if err = sink.Close(); err != nil {
		t.Fatal(err)
	}
}

func newSourceServer(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeClip(t, dir, "clip.ivf")
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("not a clip"), 0o644); err != nil {
		t.Fatal(err)
	}
	ts, _ := newTestServerWith(t, Config{SourceDir: dir})
	return ts.URL
}

func TestWebRTCSendsSource(t *testing.T) {
	url := newSourceServer(t)
	client := newWebRTCReceiver(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := openWebRTCSource(ctx, &signaling.Client{BaseURL: url}, client.webrtcClient, "clip.ivf")
	if err != nil {
		t.Fatal(err)
	}
	if err = client.SetAnswer(resp.WebRTC.SDP); err != nil {
		t.Fatal(err)
	}
	select {
	case codec := <-client.codec:
		if codec != mrtp.VP8 {
			t.Fatalf("received %v, want %v", codec, mrtp.VP8)
		}
	case <-ctx.Done():
		t.Fatal("no track received")
	}
	select {
	case <-client.ended:
	case <-ctx.Done():
		t.Fatal("track did not end with the clip")
	}
	if client.discard.Packets() < clipFrames {
		t.Fatalf("received %v packets, want at least %v", client.discard.Packets(), clipFrames)
	}
}

func TestRTPUDPSendsSource(t *testing.T) {
	url := newSourceServer(t)
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	request := rtpUDPRequest(signaling.DirectionRecv, conn.LocalAddr().String())
	request.Source = "clip.ivf"
	resp, err := (&signaling.Client{BaseURL: url}).Open(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if resp.RTP.Codec != "VP8" || resp.RTP.PayloadType != mrtp.DefaultPayloadType {
		t.Fatalf("response announces %v/%v, want VP8/%v", resp.RTP.Codec, resp.RTP.PayloadType, mrtp.DefaultPayloadType)
	}

	if err = conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 1500)
	for range clipFrames {
		n, err := conn.Read(buf)
		if err != nil {
			t.Fatal(err)
		}
		var packet rtp.Packet
		if err = packet.Unmarshal(buf[:n]); err != nil {
			t.Fatal(err)
		}
		if packet.PayloadType != resp.RTP.PayloadType {
			t.Fatalf("received payload type %v, want %v", packet.PayloadType, resp.RTP.PayloadType)
		}
	}
}

func TestRejectsSource(t *testing.T) {
	url := newSourceServer(t)
	withoutSources, _ := newTestServer(t)
	ctx := context.Background()

	for _, tc := range []struct {
		name   string
		url    string
		source string
	}{
		{"no source dir", withoutSources.URL, "clip.ivf"},
		{"parent", url, "../clip.ivf"},
		{"subdirectory", url, "sub/clip.ivf"},
		{"absolute", url, "/etc/passwd"},
		{"missing", url, "missing.ivf"},
		{"not ivf", url, "notes.txt"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := openWebRTCSource(ctx, &signaling.Client{BaseURL: tc.url}, newWebRTCReceiver(t).webrtcClient, tc.source)
			if err == nil || !strings.Contains(err.Error(), "400") {
				t.Fatalf("opening source %q returned %v, want 400", tc.source, err)
			}
		})
	}

	t.Run("webrtc client sends", func(t *testing.T) {
		client, _ := newWebRTCClient(t)
		_, err := openWebRTCSource(ctx, &signaling.Client{BaseURL: url}, client, "clip.ivf")
		if err == nil || !strings.Contains(err.Error(), "400") {
			t.Fatalf("opening a source on a sending offer returned %v, want 400", err)
		}
	})
	t.Run("rtp-udp client sends", func(t *testing.T) {
		request := rtpUDPRequest(signaling.DirectionSend, "")
		request.Source = "clip.ivf"
		_, err := (&signaling.Client{BaseURL: url}).Open(ctx, request)
		if err == nil || !strings.Contains(err.Error(), "400") {
			t.Fatalf("opening a source on a sending rtp-udp session returned %v, want 400", err)
		}
	})
}

func TestRecordsWebRTCTrack(t *testing.T) {
	dir := t.TempDir()
	ts, srv := newTestServerWith(t, Config{SinkDir: dir})
	client := newWebRTCTransport(t)
	track, err := client.AddLocalTrackWithCodec(mrtp.VP8.MimeType())
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	signaler := &signaling.Client{BaseURL: ts.URL}
	resp := openWebRTC(t, ctx, signaler, client)
	sess := session[*webrtcSession](t, srv, resp.ID)
	sendFrames(t, func(seq uint16) { writeRTP(t, track, seq) }, sess.frames)
	if err = signaler.Close(ctx, resp.ID); err != nil {
		t.Fatal(err)
	}

	file, err := os.Open(filepath.Join(dir, resp.ID+".ivf"))
	if err != nil {
		t.Fatal(err)
	}
	source, err := mediafile.NewIVFSource(file)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	if codec := source.Format().(mrtp.EncodedVideo).Codec; codec != mrtp.VP8 {
		t.Fatalf("recorded %v, want %v", codec, mrtp.VP8)
	}
}
