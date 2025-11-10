package subcmd

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"image"
	"log/slog"
	"net"
	"os"
	"time"

	"github.com/julienschmidt/httprouter"
	"github.com/mengelbart/mrtp"
	"github.com/mengelbart/mrtp/cmdmain"
	"github.com/mengelbart/mrtp/internal/http"
	"github.com/mengelbart/mrtp/webrtc"
	"github.com/mengelbart/y4m"
)

func init() {
	cmdmain.RegisterSubCmd("send2", func() cmdmain.SubCmd { return new(Send2) })
}

type Send2 struct{}

// Exec implements cmdmain.SubCmd.
func (s *Send2) Exec(cmd string, args []string) error {
	fs := flag.NewFlagSet("send2", flag.ExitOnError)
	path := fs.String("stream", "", "Input file")
	offer := fs.Bool("offer", false, "Act as WebRTC offerer")
	localAddr := fs.String("local-addr", "localhost", "Local signaling server address")
	remoteAddr := fs.String("remote-addr", "localhost", "Remote signaling server address")
	localPort := fs.String("local-port", "8080", "Local signaling server port")
	remotePort := fs.String("remote-port", "9090", "Remote signaling server port")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Run a sender

Usage:
	%s send2 [flags]

Flags:
`, cmd)
		fs.PrintDefaults()
		fmt.Fprintln(os.Stderr)
	}
	fs.Parse(args)

	connectedCtx, cancelConnectedCtx := context.WithCancel(context.Background())
	defer cancelConnectedCtx()
	onConnectedCallback := func() {
		cancelConnectedCtx()
	}
	transport, err := setupWebRTCTransport(
		*offer,
		*localAddr,
		*remoteAddr,
		*localPort,
		*remotePort,
		webrtc.RegisterDefaultCodecs(),
		webrtc.OnConnected(onConnectedCallback),
	)
	if err != nil {
		return err
	}
	defer transport.Close()

	stream, err := transport.AddLocalTrackWithCodec("video/VP8")
	if err != nil {
		return err
	}
	sink := mrtp.WriterFunc(func(b []byte, a mrtp.Attributes) error {
		_, err := stream.Write(b)
		if err != nil {
			panic(err)
		}
		return err
	})

	file, err := os.Open(*path)
	if err != nil {
		return err
	}
	defer file.Close()

	reader, streamHeader, err := y4m.NewReader(file)
	if err != nil {
		return err
	}

	i := mrtp.Info{
		Width:       uint(streamHeader.Width),
		Height:      uint(streamHeader.Height),
		TimebaseNum: streamHeader.FrameRate.Numerator,
		TimebaseDen: streamHeader.FrameRate.Denominator,
	}
	encoder := mrtp.NewVP8Encoder()
	packetizer := &mrtp.RTPPacketizerFactory{
		MTU:       1420,
		PT:        96,
		SSRC:      0,
		ClockRate: 90_000,
	}
	pacer := &mrtp.FrameSpacer{}
	writer, err := mrtp.Chain(i, sink, pacer, packetizer, encoder)
	if err != nil {
		return err
	}

	fps := float64(i.TimebaseNum) / float64(i.TimebaseDen)
	frameDuration := time.Duration(float64(time.Second) / fps)
	select {
	case <-connectedCtx.Done():
	case <-time.After(5 * time.Second):
		return errors.New("timeout while waiting for WebRTC connection")
	}
	ticker := time.NewTicker(frameDuration)
	var next time.Time
	for range ticker.C {
		now := time.Now()
		lateness := now.Sub(next)
		next = now.Add(frameDuration)
		slog.Info("FRAME", "duration", frameDuration, "next", now.Add(frameDuration), "lateness", lateness)
		frame, _, err := reader.ReadNextFrame()
		if err != nil {
			return err
		}
		ioDone := time.Now()
		slog.Info("read frame from disk", "latency", ioDone.Sub(now))
		csr := convertSubsampleRatio(streamHeader.ChromaSubsampling)
		if err = writer.Write(frame, mrtp.Attributes{
			mrtp.ChromaSubsampling: csr,
		}); err != nil {
			return err
		}
	}
	return nil
}

// Help implements cmdmain.SubCmd.
func (s *Send2) Help() string {
	return "Run a sender, differs from send by using the Go pipeline architecture instead of Gstreamer"
}

func setupWebRTCTransport(offer bool, localAddr, remoteAddr, localPort, remotePort string, opts ...webrtc.Option) (*webrtc.Transport, error) {
	signaler := webrtc.NewHTTPClientSignaler(fmt.Sprintf("http://%v:%v", remoteAddr, remotePort))
	transport, err := webrtc.NewTransport(
		signaler,
		offer,
		opts...,
	)
	if err != nil {
		return nil, err
	}
	signalingHandler := webrtc.NewHTTPSignalingHandler(transport)
	router := httprouter.New()
	router.HandlerFunc("POST", "/candidate", signalingHandler.HandleCandidate)
	router.HandlerFunc("POST", "/session_description", signalingHandler.HandleSessionDescription)

	host := net.JoinHostPort(localAddr, localPort)
	server, err := http.NewServer(
		http.H1Address(host),
		http.ListenH2(false),
		http.ListenH3(false),
		http.RedirectH1ToH3(false),
		http.Handle(router),
	)
	if err != nil {
		return nil, err
	}
	go server.ListenAndServe()
	return transport, nil
}

func convertSubsampleRatio(s y4m.ChromaSubsamplingType) image.YCbCrSubsampleRatio {
	switch s {
	case y4m.CST411:
		return image.YCbCrSubsampleRatio411
	case y4m.CST420:
		return image.YCbCrSubsampleRatio420
	case y4m.CST420jpeg:
		return image.YCbCrSubsampleRatio420
	case y4m.CST420mpeg2:
		return image.YCbCrSubsampleRatio420
	case y4m.CST420paldv:
		return image.YCbCrSubsampleRatio420
	case y4m.CST422:
		return image.YCbCrSubsampleRatio422
	case y4m.CST444:
		return image.YCbCrSubsampleRatio444
	case y4m.CST444Alpha:
		return image.YCbCrSubsampleRatio444
	default:
		panic(fmt.Sprintf("unexpected y4m.ChromaSubsamplingType: %#v", s))
	}
}
