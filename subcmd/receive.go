package subcmd

import (
	"flag"
	"fmt"
	"os"

	"github.com/mengelbart/mrtp/gstreamer"
	"github.com/mengelbart/mrtp/rtp"
)

type receiveFlags struct {
	remote       string
	local        string
	rtpPort      int
	rtcpSendPort int
	rtcpRecvPort int
}

func Receive(cmd string, args []string) error {
	var rf receiveFlags

	flags := flag.NewFlagSet("receive", flag.ExitOnError)
	flags.StringVar(&rf.remote, "remote", "127.0.0.1", "Remote UDP Address")
	flags.StringVar(&rf.local, "local", "127.0.0.1", "Local UDP Address")
	flags.IntVar(&rf.rtpPort, "rtp-port", 5000, "UDP Port number for outgoing RTP stream")
	flags.IntVar(&rf.rtcpSendPort, "rtcp-send-port", 5002, "UDP port number for outgoing RTCP stream")
	flags.IntVar(&rf.rtcpRecvPort, "rtcp-recv-port", 5001, "UDP port number for incoming RTCP stream")

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

	transport, err := gstreamer.NewUDPTransport(rf.remote,
		map[gstreamer.ID]gstreamer.PortNumber{
			0: gstreamer.PortNumber(rf.rtcpSendPort),
		},
		map[gstreamer.ID]gstreamer.PortNumber{
			0: gstreamer.PortNumber(rf.rtcpRecvPort),
			1: gstreamer.PortNumber(rf.rtpPort),
		},
	)
	if err != nil {
		return err
	}

	sink, err := gstreamer.NewRTPStreamSink("rtp-stream-sink")
	if err != nil {
		return err
	}

	receiver, err := rtp.NewReceiver(transport, map[int]*gstreamer.RTPStreamSink{1: sink})
	if err != nil {
		return err
	}
	return receiver.Run()
}
