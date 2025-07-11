package subcmd

import (
	"errors"
	"flag"
	"fmt"
	"math"
	"os"

	"github.com/mengelbart/mrtp/gstreamer"
	"github.com/mengelbart/mrtp/roq"
)

type sendFlags struct {
	remote        string
	local         string
	sendVideoFile string
	rtpSendPort   uint
	rtcpSendPort  uint
	rtcpRecvPort  uint
	roqServer     bool
	roqClient     bool
	gstScream     bool
}

func Send(cmd string, args []string) error {
	var sf sendFlags

	flags := flag.NewFlagSet("send", flag.ExitOnError)
	flags.StringVar(&sf.sendVideoFile, "file", "videotestsrc", "Which video to send")
	flags.StringVar(&sf.remote, "remote", "127.0.0.1", "Remote UDP Address")
	flags.StringVar(&sf.local, "local", "127.0.0.1", "Local UDP Address")
	flags.UintVar(&sf.rtpSendPort, "rtp-port", 5000, "UDP Port number for outgoing RTP stream")
	flags.UintVar(&sf.rtcpSendPort, "rtcp-send-port", 5001, "UDP port number for outgoing RTCP stream")
	flags.UintVar(&sf.rtcpRecvPort, "rtcp-recv-port", 5002, "UDP port number for incoming RTCP stream")
	flags.BoolVar(&sf.roqServer, "roq-server", false, "Run a RoQ server instead of using UDP. UDP related flags are ignored and <local> is used as the address to run the QUIC server on.")
	flags.BoolVar(&sf.roqClient, "roq-client", false, "Run a RoQ client instead of using UDP. UDP related flags are ignored and <remote> is as the server address to connect to.")
	flags.BoolVar(&sf.gstScream, "gst-scream", false, "Run SCReAM Gstreamer element")

	flags.Usage = func() {
		fmt.Fprintf(os.Stderr, `Run a sender pipeline

Usage:
	%s send [flags]

Flags:
`, cmd)
		flags.PrintDefaults()
		fmt.Fprintln(os.Stderr)
	}
	flags.Parse(args)

	if len(flags.Args()) > 1 {
		fmt.Printf("error: unknown extra arguments: %v\n", flag.Args()[1:])
		flags.Usage()
		os.Exit(1)
	}

	for _, p := range []uint{
		sf.rtcpRecvPort,
		sf.rtcpSendPort,
		sf.rtpSendPort,
	} {
		if p > math.MaxUint32 {
			return fmt.Errorf("invalid port number: %v", p)
		}
	}
	if sf.roqClient && sf.roqServer {
		return errors.New("cannot run RoQ server and client simultaneously")
	}

	streamSourceOpts := make([]gstreamer.StreamSourceOption, 0)
	if sf.sendVideoFile != "videotestsrc" {
		// check if file exists
		if _, err := os.Stat(sf.sendVideoFile); errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("file does not exist: %v", sf.sendVideoFile)
		}

		streamSourceOpts = append(streamSourceOpts, gstreamer.StreamSourceFileSourceLocation(sf.sendVideoFile))
		streamSourceOpts = append(streamSourceOpts, gstreamer.StreamSourceType(gstreamer.Filesrc))
	}

	source, err := gstreamer.NewStreamSource("rtp-stream-source", streamSourceOpts...)
	if err != nil {
		return err
	}

	sender, err := gstreamer.NewRTPBin()
	if err != nil {
		return err
	}
	sender.SetTargetRateEncoder = source.SetBitrate

	if sf.roqServer || sf.roqClient {
		transport, err := roq.New(
			roq.WithRole(roq.Role(sf.roqServer)),
		)
		if err != nil {
			return err
		}

		rtpSink, err := transport.NewSendFlow(uint64(sf.rtpSendPort))
		if err != nil {
			return err
		}
		if err = sender.AddRTPTransportSink(0, rtpSink); err != nil {
			return err
		}
		if err = sender.AddRTPSourceStreamGst(0, source.Element(), sf.gstScream); err != nil {
			return err
		}

		rtcpSink, err := transport.NewSendFlow(uint64(sf.rtcpSendPort))
		if err != nil {
			return err
		}
		if err = sender.SendRTCPForStream(0, rtcpSink); err != nil {
			return err
		}

		rtcpSrc, err := transport.NewReceiveFlow(uint64(sf.rtcpRecvPort))
		if err != nil {
			return err
		}
		if err = sender.ReceiveRTCPFrom(rtcpSrc); err != nil {
			return err
		}

	} else {
		rtpSink, err := gstreamer.NewUDPSink(sf.remote, uint32(sf.rtpSendPort))
		if err != nil {
			return err
		}
		if err = sender.AddRTPTransportSinkGst(0, rtpSink.GetGstElement()); err != nil {
			return err
		}
		if err = sender.AddRTPSourceStreamGst(0, source.Element(), sf.gstScream); err != nil {
			return err
		}

		rtcpSink, err := gstreamer.NewUDPSink(sf.remote, uint32(sf.rtcpSendPort))
		if err != nil {
			return err
		}
		if err = sender.SendRTCPForStreamGst(0, rtcpSink.GetGstElement()); err != nil {
			return err
		}

		rtcpSrc, err := gstreamer.NewUDPSrc(sf.local, uint32(sf.rtcpRecvPort))
		if err != nil {
			return err
		}
		if err = sender.ReceiveRTCPFromGst(rtcpSrc.GetGstElement()); err != nil {
			return err
		}
	}

	return sender.Run()
}
