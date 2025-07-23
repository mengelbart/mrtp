package subcmd

import (
	"errors"
	"flag"
	"fmt"
	"math"
	"os"

	"github.com/mengelbart/mrtp/cmdmain"
	"github.com/mengelbart/mrtp/flags"
	"github.com/mengelbart/mrtp/gstreamer"
	"github.com/mengelbart/mrtp/roq"
)

func init() {
	cmdmain.RegisterSubCmd("send", func() cmdmain.SubCmd { return new(Send) })
}

// BitrateAdapter is the interface implemented by source streams that can adapt
// their bitrate to a target rate.
type BitrateAdapter interface {
	// SetBitrate sets the target bitrate for the stream source.
	SetBitrate(uint) error
}

var MakeStreamSource = func(name string) (gstreamer.RTPSourceBin, error) {
	streamSourceOpts := make([]gstreamer.StreamSourceOption, 0)
	if flags.SendVideoFile != "videotestsrc" {
		// check if file exists
		if _, err := os.Stat(flags.SendVideoFile); errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("file does not exist: %v", flags.SendVideoFile)
		}

		streamSourceOpts = append(streamSourceOpts, gstreamer.StreamSourceFileSourceLocation(flags.SendVideoFile))
		streamSourceOpts = append(streamSourceOpts, gstreamer.StreamSourceType(gstreamer.Filesrc))
	}
	return gstreamer.NewStreamSource(name, streamSourceOpts...)
}

var (
	gstSCReAM bool
)

type Send struct{}

func (s *Send) Help() string {
	return "Run sender pipeline"
}

func (s *Send) Exec(cmd string, args []string) error {
	fs := flag.NewFlagSet("send", flag.ExitOnError)
	flags.RegisterInto(fs, []flags.FlagName{
		flags.LocalAddrFlag,
		flags.RemoteAddrFlag,
		flags.RTPPortFlag,
		flags.RTCPSendPortFlag,
		flags.RTCPRecvPortFlag,
		flags.RoQServerFlag,
		flags.RoQClientFlag,
		flags.SendVideoFileFlag,
		flags.TraceRTPSendFlag,
	}...)
	fs.BoolVar(&gstSCReAM, "gst-scream", false, "Run SCReAM Gstreamer element")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Run a sender pipeline

Usage:
	%s send [flags]

Flags:
`, cmd)
		fs.PrintDefaults()
		fmt.Fprintln(os.Stderr)
	}
	fs.Parse(args)

	if len(fs.Args()) > 1 {
		fmt.Printf("error: unknown extra arguments: %v\n", flag.Args()[1:])
		fs.Usage()
		os.Exit(1)
	}

	for _, p := range []uint{
		flags.RTCPRecvPort,
		flags.RTCPSendPort,
		flags.RTPPort,
	} {
		if p > math.MaxUint32 {
			return fmt.Errorf("invalid port number: %v", p)
		}
	}
	if flags.RoQClient && flags.RoQServer {
		return errors.New("cannot run RoQ server and client simultaneously")
	}

	source, err := MakeStreamSource("rtp-stream-source")
	if err != nil {
		return err
	}

	sender, err := gstreamer.NewRTPBin()
	if err != nil {
		return err
	}
	if ba, ok := source.(BitrateAdapter); ok {
		sender.SetTargetRateEncoder = ba.SetBitrate
	}

	if flags.RoQServer || flags.RoQClient {
		transport, err := roq.New(
			roq.WithRole(roq.Role(flags.RoQServer)),
		)
		if err != nil {
			return err
		}

		rtpSink, err := transport.NewSendFlow(uint64(flags.RTPPort))
		if err != nil {
			return err
		}
		if err = sender.AddRTPTransportSink(0, rtpSink); err != nil {
			return err
		}
		if err = sender.AddRTPSourceStreamGst(0, source, gstSCReAM); err != nil {
			return err
		}

		rtcpSink, err := transport.NewSendFlow(uint64(flags.RTCPSendPort))
		if err != nil {
			return err
		}
		if err = sender.SendRTCPForStream(0, rtcpSink); err != nil {
			return err
		}

		rtcpSrc, err := transport.NewReceiveFlow(uint64(flags.RTCPRecvPort))
		if err != nil {
			return err
		}
		if err = sender.ReceiveRTCPFrom(rtcpSrc); err != nil {
			return err
		}

	} else {
		rtpSink, err := gstreamer.NewUDPSink(flags.RemoteAddr, uint32(flags.RTPPort), gstreamer.EnabelUDPSinkPadProbe(flags.TraceRTPSend))
		if err != nil {
			return err
		}
		if err = sender.AddRTPTransportSinkGst(0, rtpSink.GetGstElement()); err != nil {
			return err
		}
		if err = sender.AddRTPSourceStreamGst(0, source, gstSCReAM); err != nil {
			return err
		}

		rtcpSink, err := gstreamer.NewUDPSink(flags.RemoteAddr, uint32(flags.RTCPSendPort))
		if err != nil {
			return err
		}
		if err = sender.SendRTCPForStreamGst(0, rtcpSink.GetGstElement()); err != nil {
			return err
		}

		rtcpSrc, err := gstreamer.NewUDPSrc(flags.LocalAddr, uint32(flags.RTCPRecvPort))
		if err != nil {
			return err
		}
		if err = sender.ReceiveRTCPFromGst(rtcpSrc.GetGstElement()); err != nil {
			return err
		}
	}

	return sender.Run()
}
