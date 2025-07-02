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

type receiveFlags struct {
	remote       string
	local        string
	rtpRecvPort  uint
	rtcpSendPort uint
	rtcpRecvPort uint
	roqServer    bool
	roqClient    bool
}

func Receive(cmd string, args []string) error {
	var rf receiveFlags

	flags := flag.NewFlagSet("receive", flag.ExitOnError)
	flags.StringVar(&rf.remote, "remote", "127.0.0.1", "Remote UDP Address")
	flags.StringVar(&rf.local, "local", "127.0.0.1", "Local UDP Address")
	flags.UintVar(&rf.rtpRecvPort, "rtp-port", 5000, "UDP Port number for outgoing RTP stream")
	flags.UintVar(&rf.rtcpSendPort, "rtcp-send-port", 5002, "UDP port number for outgoing RTCP stream")
	flags.UintVar(&rf.rtcpRecvPort, "rtcp-recv-port", 5001, "UDP port number for incoming RTCP stream")
	flags.BoolVar(&rf.roqServer, "roq-server", false, "Run a RoQ server instead of using UDP. UDP related flags are ignored and <local> is used as the address to run the QUIC server on.")
	flags.BoolVar(&rf.roqClient, "roq-client", false, "Run a RoQ client instead of using UDP. UDP related flags are ignored and <remote> is as the server address to connect to.")

	flags.Usage = func() {
		fmt.Fprintf(os.Stderr, `Run a receiver pipeline

Usage:
	%v receive [flags]

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
		rf.rtcpRecvPort,
		rf.rtcpSendPort,
		rf.rtpRecvPort,
	} {
		if p > math.MaxUint32 {
			return fmt.Errorf("invalid port number: %v", p)
		}
	}
	if rf.roqClient && rf.roqServer {
		return errors.New("cannot run RoQ server and client simultaneously")
	}

	receiver, err := gstreamer.NewRTPBin()
	if err != nil {
		return err
	}

	sink, err := gstreamer.NewStreamSink("rtp-stream-sink")
	if err != nil {
		return err
	}

	if rf.roqServer || rf.roqClient {
		transport, err := roq.New(
			roq.WithRole(roq.Role(rf.roqServer)),
		)
		if err != nil {
			return err
		}

		rtpSrc, err := transport.NewReceiveFlow(uint64(rf.rtpRecvPort))
		if err != nil {
			return err
		}
		if err = receiver.AddRTPReceiveStreamSinkGst(0, sink); err != nil {
			return err
		}
		if err = receiver.ReceiveRTPStreamFrom(0, rtpSrc); err != nil {
			return err
		}

		rtcpSink, err := transport.NewSendFlow(uint64(rf.rtcpSendPort))
		if err != nil {
			return err
		}
		if err = receiver.SendRTCPForStream(0, rtcpSink); err != nil {
			return err
		}

		rtcpSrc, err := transport.NewReceiveFlow(uint64(rf.rtcpRecvPort))
		if err != nil {
			return err
		}
		if err = receiver.ReceiveRTCPFrom(rtcpSrc); err != nil {
			return err
		}

	} else {
		rtpSrc, err := gstreamer.NewUDPSrc(rf.local, uint32(rf.rtpRecvPort))
		if err != nil {
			return err
		}
		if err = receiver.AddRTPReceiveStreamSinkGst(0, sink); err != nil {
			return err
		}
		if err = receiver.ReceiveRTPStreamFromGst(0, rtpSrc.GetGstElement()); err != nil {
			return err
		}

		rtcpSink, err := gstreamer.NewUDPSink(rf.remote, uint32(rf.rtcpSendPort))
		if err != nil {
			return err
		}
		if err = receiver.SendRTCPForStreamGst(0, rtcpSink.GetGstElement()); err != nil {
			return err
		}

		rtcpSrc, err := gstreamer.NewUDPSrc(rf.local, uint32(rf.rtcpRecvPort))
		if err != nil {
			return err
		}
		if err = receiver.ReceiveRTCPFromGst(rtcpSrc.GetGstElement()); err != nil {
			return err
		}
	}

	return receiver.Run()
}
