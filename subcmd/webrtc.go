package subcmd

import (
	"flag"
	"fmt"
	"net"
	"os"

	"github.com/julienschmidt/httprouter"
	"github.com/mengelbart/mrtp/gstreamer"
	"github.com/mengelbart/mrtp/internal/http"
	"github.com/mengelbart/mrtp/webrtc"
)

type webrtcFlags struct {
	localAddr      string
	localPort      string
	remoteAddr     string
	remotePort     string
	offer          bool
	sendVideoTrack bool
}

func WebRTC(cmd string, args []string) error {
	var wf webrtcFlags
	flags := flag.NewFlagSet("webrtc", flag.ExitOnError)

	flags.StringVar(&wf.localAddr, "local-address", "localhost", "Local address of HTTP signaling server to listen on")
	flags.StringVar(&wf.localPort, "local-port", "8080", "Local port of HTTP signaling server to listen on")
	flags.StringVar(&wf.remoteAddr, "remote-addr", "localhost", "Remote address of HTTP signaling server to connect to")
	flags.StringVar(&wf.remotePort, "remote-port", "8080", "Remote Port of HTTP signaling server to connect to")
	flags.BoolVar(&wf.offer, "offer", false, "Act as the offerer for WebRTC signaling")
	flags.BoolVar(&wf.sendVideoTrack, "send-track", false, "Send a media track to the peer")

	flags.Usage = func() {
		fmt.Fprintf(os.Stderr, `Run a WebRTC pipeline

Usage:
	%v webrtc [flags]
`, cmd)
		flags.PrintDefaults()
		fmt.Fprintln(os.Stderr)
	}
	flags.Parse(args)

	pipeline, err := gstreamer.NewRTPBin()
	if err != nil {
		return err
	}

	signaler := webrtc.NewHTTPClientSignaler(fmt.Sprintf("http://%v:%v", wf.remoteAddr, wf.remotePort))
	transport, err := webrtc.NewTransport(signaler, wf.offer, webrtc.OnTrack(func(receiver *webrtc.RTPReceiver) {
		sink, newSinkErr := gstreamer.NewStreamSink("rtp-stream-sink", gstreamer.StreamSinkPayloadType(int(receiver.PayloadType())))
		if newSinkErr != nil {
			panic(err)
		}
		if pipelineErr := pipeline.AddRTPReceiveStreamSinkGst(0, sink); pipelineErr != nil {
			panic(pipelineErr)
		}
		if pipelineErr := pipeline.ReceiveRTPStreamFrom(0, receiver); pipelineErr != nil {
			panic(pipelineErr)
		}
		if pipelineErr := pipeline.ReceiveRTCPFrom(receiver.RTCPReceiver()); pipelineErr != nil {
			panic(pipelineErr)
		}
	}))
	if err != nil {
		return err
	}
	signalingHandler := webrtc.NewHTTPSignalingHandler(transport)
	router := httprouter.New()
	router.HandlerFunc("POST", "/candidate", signalingHandler.HandleCandidate)
	router.HandlerFunc("POST", "/session_description", signalingHandler.HandleSessionDescription)

	host := net.JoinHostPort(wf.localAddr, wf.localPort)
	s, err := http.NewServer(
		http.H1Address(host),
		http.ListenH2(false),
		http.ListenH3(false),
		http.RedirectH1ToH3(false),
		http.Handle(router),
	)
	if err != nil {
		return err
	}
	go s.ListenAndServe()

	if wf.sendVideoTrack {
		var rtpSink *webrtc.RTPSender
		rtpSink, err = transport.AddLocalTrack()
		if err != nil {
			return err
		}
		if err = pipeline.AddRTPTransportSink(0, rtpSink); err != nil {
			return err
		}
		var source *gstreamer.StreamSource
		source, err = gstreamer.NewStreamSource("rtp-stream-source")
		if err != nil {
			return err
		}
		if err = pipeline.AddRTPStreamGst(0, source); err != nil {
			return err
		}
		if err = pipeline.ReceiveRTCPFrom(rtpSink.RTCPReceiver()); err != nil {
			return err
		}
	} else {
		if err = transport.AddRemoteVideoTrack(); err != nil {
			return err
		}
	}

	if err = pipeline.SendRTCPForStream(0, transport); err != nil {
		return err
	}

	return pipeline.Run()
}
