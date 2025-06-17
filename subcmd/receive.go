package subcmd

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/mengelbart/mrtp"
	"github.com/mengelbart/mrtp/gstreamer"
	"github.com/mengelbart/mrtp/roq"
	"github.com/mengelbart/mrtp/rtp"
)

type receiveFlags struct {
	remote       string
	local        string
	rtpPort      int
	rtcpSendPort int
	rtcpRecvPort int
	roqServer    bool
	roqClient    bool
}

func Receive(cmd string, args []string) error {
	var rf receiveFlags

	flags := flag.NewFlagSet("receive", flag.ExitOnError)
	flags.StringVar(&rf.remote, "remote", "127.0.0.1", "Remote UDP Address")
	flags.StringVar(&rf.local, "local", "127.0.0.1", "Local UDP Address")
	flags.IntVar(&rf.rtpPort, "rtp-port", 5000, "UDP Port number for outgoing RTP stream")
	flags.IntVar(&rf.rtcpSendPort, "rtcp-send-port", 5002, "UDP port number for outgoing RTCP stream")
	flags.IntVar(&rf.rtcpRecvPort, "rtcp-recv-port", 5001, "UDP port number for incoming RTCP stream")
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

	var transport mrtp.Transport
	var err error
	if rf.roqClient && rf.roqServer {
		return errors.New("cannot run RoQ server and client simultaneously")
	}
	if rf.roqServer || rf.roqClient {
		transport, err = roq.New(
			roq.WithRole(roq.Role(rf.roqServer)),
			roq.AddSender(0),
			roq.AddReceiver(0),
			roq.AddReceiver(1),
		)
	} else {
		transport, err = gstreamer.NewUDPTransport(rf.remote,
			map[gstreamer.ID]gstreamer.PortNumber{
				0: gstreamer.PortNumber(rf.rtcpSendPort),
			},
			map[gstreamer.ID]gstreamer.PortNumber{
				0: gstreamer.PortNumber(rf.rtpPort),
				1: gstreamer.PortNumber(rf.rtcpRecvPort),
			},
		)
	}
	if err != nil {
		return err
	}
	if transport == nil {
		return errors.New("invalid transport configuration")
	}

	sink, err := gstreamer.NewRTPStreamSink("rtp-stream-sink")
	if err != nil {
		return err
	}

	receiver, err := rtp.NewReceiver(transport, map[int]*gstreamer.RTPStreamSink{0: sink})
	if err != nil {
		return err
	}
	return receiver.Run()
}
