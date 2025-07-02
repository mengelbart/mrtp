package subcmd

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/mengelbart/mrtp"
	"github.com/mengelbart/mrtp/gstreamer"
	"github.com/mengelbart/mrtp/roq"
)

type sendFlags struct {
	remote       string
	local        string
	rtpPort      int
	rtcpSendPort int
	rtcpRecvPort int
	roqServer    bool
	roqClient    bool
}

func Send(cmd string, args []string) error {
	var sf sendFlags

	flags := flag.NewFlagSet("send", flag.ExitOnError)
	flags.StringVar(&sf.remote, "remote", "127.0.0.1", "Remote UDP Address")
	flags.StringVar(&sf.local, "local", "127.0.0.1", "Local UDP Address")
	flags.IntVar(&sf.rtpPort, "rtp-port", 5000, "UDP Port number for outgoing RTP stream")
	flags.IntVar(&sf.rtcpSendPort, "rtcp-send-port", 5001, "UDP port number for outgoing RTCP stream")
	flags.IntVar(&sf.rtcpRecvPort, "rtcp-recv-port", 5002, "UDP port number for incoming RTCP stream")
	flags.BoolVar(&sf.roqServer, "roq-server", false, "Run a RoQ server instead of using UDP. UDP related flags are ignored and <local> is used as the address to run the QUIC server on.")
	flags.BoolVar(&sf.roqClient, "roq-client", false, "Run a RoQ client instead of using UDP. UDP related flags are ignored and <remote> is as the server address to connect to.")

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

	var transport mrtp.Transport
	var err error
	if sf.roqClient && sf.roqServer {
		return errors.New("cannot run RoQ server and client simultaneously")
	}
	if sf.roqServer || sf.roqClient {
		transport, err = roq.New(
			roq.WithRole(roq.Role(sf.roqServer)),
			roq.AddSender(0),
			roq.AddSender(1),
			roq.AddReceiver(0),
		)
	} else {
		transport, err = gstreamer.NewUDPTransport(sf.remote,
			map[gstreamer.ID]gstreamer.PortNumber{
				0: gstreamer.PortNumber(sf.rtpPort),
				1: gstreamer.PortNumber(sf.rtcpSendPort),
			},
			map[gstreamer.ID]gstreamer.PortNumber{
				0: gstreamer.PortNumber(sf.rtcpRecvPort),
			},
		)
	}
	if err != nil {
		return err
	}
	if transport == nil {
		return errors.New("invalid transport configuration")
	}

	sender, err := gstreamer.NewRTPBin()
	if err != nil {
		return err
	}

	source, err := gstreamer.NewStreamSource("rtp-stream-source")
	if err != nil {
		return err
	}

	if err = sender.SendRTCPForStreamToGst(0, transport.GetSink(1)); err != nil {
		return err
	}
	if err = sender.SendRTPStreamToGst(0, source, transport.GetSink(0)); err != nil {
		return err
	}
	if err = sender.ReceiveRTCPFromGst(transport.GetSrc(0)); err != nil {
		return err
	}

	return sender.Run()
}
