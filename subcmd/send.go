package subcmd

import (
	"flag"
	"fmt"
	"os"

	"github.com/mengelbart/mrtp/gstreamer"
	"github.com/mengelbart/mrtp/rtp"
)

type sendFlags struct {
	remote       string
	local        string
	rtpPort      int
	rtcpSendPort int
	rtcpRecvPort int
}

func Send(cmd string, args []string) error {
	var sf sendFlags

	flags := flag.NewFlagSet("send", flag.ExitOnError)
	flags.StringVar(&sf.remote, "remote", "127.0.0.1", "Remote UDP Address")
	flags.StringVar(&sf.local, "local", "127.0.0.1", "Local UDP Address")
	flags.IntVar(&sf.rtpPort, "rtp-port", 5000, "UDP Port number for outgoing RTP stream")
	flags.IntVar(&sf.rtcpSendPort, "rtcp-send-port", 5001, "UDP port number for outgoing RTCP stream")
	flags.IntVar(&sf.rtcpRecvPort, "rtcp-recv-port", 5002, "UDP port number for incoming RTCP stream")

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

	source, err := gstreamer.NewRTPStreamSource("rtp-stream-source")
	if err != nil {
		return err
	}

	transport, err := gstreamer.NewUDPTransport(sf.remote,
		map[gstreamer.ID]gstreamer.PortNumber{
			0: gstreamer.PortNumber(sf.rtcpSendPort),
			1: gstreamer.PortNumber(sf.rtpPort),
		},
		map[gstreamer.ID]gstreamer.PortNumber{
			0: gstreamer.PortNumber(sf.rtcpRecvPort),
		},
	)
	if err != nil {
		return err
	}

	sender, err := rtp.NewSender(transport, map[int]*gstreamer.RTPStreamSource{1: source})
	if err != nil {
		return err
	}
	return sender.Run()
}
