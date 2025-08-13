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

type StreamSourceFactory interface {
	ConfigureFlags(*flag.FlagSet)
	MakeStreamSource(name string) (gstreamer.RTPSourceBin, error)
}

type gstreamerVideoStreamSourceFactory struct {
}

func (f *gstreamerVideoStreamSourceFactory) ConfigureFlags(fs *flag.FlagSet) {
	flags.RegisterInto(fs, []flags.FlagName{
		flags.LocationFlag,
	}...)
}

func (f *gstreamerVideoStreamSourceFactory) MakeStreamSource(name string) (gstreamer.RTPSourceBin, error) {
	streamSourceOpts := make([]gstreamer.StreamSourceOption, 0)
	if flags.Location != "videotestsrc" {
		// check if file exists
		if _, err := os.Stat(flags.Location); errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("file does not exist: %v", flags.Location)
		}

		streamSourceOpts = append(streamSourceOpts, gstreamer.StreamSourceFileSourceLocation(flags.Location))
		streamSourceOpts = append(streamSourceOpts, gstreamer.StreamSourceType(gstreamer.Filesrc))
	}
	return gstreamer.NewStreamSource(name, streamSourceOpts...)
}

var DefaultStreamSourceFactory StreamSourceFactory = &gstreamerVideoStreamSourceFactory{}

var (
	gstSCReAM bool
	nada      bool
	quicCC    int
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
		flags.TraceRTPSendFlag,
	}...)
	fs.BoolVar(&gstSCReAM, "gst-scream", false, "Run SCReAM Gstreamer element")
	fs.BoolVar(&nada, "nada", false, "Run NADA") // TODO: move to flags package
	fs.IntVar(&quicCC, "quic-cc", 0, "Which quic CC to use. 0: Reno, 1: no CC and no pacer, 2: only pacer")

	DefaultStreamSourceFactory.ConfigureFlags(fs)

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

	if quicCC < 0 || quicCC > 2 {
		fmt.Printf("error: invalid quic-cc value, must be 0, 1 or 2\n")
		fs.Usage()
		os.Exit(1)
	}

	if (nada || quicCC != 0) && !(flags.RoQServer || flags.RoQClient) {
		fmt.Printf("Flags -nada and -quic-cc only valid for RoQ\n")
		fs.Usage()
		os.Exit(1)
	}

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

	source, err := DefaultStreamSourceFactory.MakeStreamSource("rtp-stream-source")
	if err != nil {
		return err
	}

	sender, err := gstreamer.NewRTPBin()
	if err != nil {
		return err
	}

	ba, ok := source.(BitrateAdapter)
	if ok {
		sender.SetTargetRateEncoder = ba.SetBitrate
	}

	if flags.RoQServer || flags.RoQClient {
		roqOptions := []roq.Option{
			roq.WithRole(roq.Role(flags.RoQServer)),
			roq.SetQuicCC(quicCC),
			roq.SetLocalAdress(flags.LocalAddr, flags.RTPPort), // TODO: which port to use?
			roq.SetRemoteAdress(flags.RemoteAddr, flags.RTPPort),
		}

		if nada {
			roqOptions = append(roqOptions, roq.EnableNADA(750_000, 150_000, 3_000_000))
		}

		transport, err := roq.New(roqOptions...)
		if err != nil {
			return err
		}
		transport.SetTargetRateEncoder = ba.SetBitrate

		rtpSink, err := transport.NewSendFlow(uint64(flags.RTPPort), flags.TraceRTPSend)
		if err != nil {
			return err
		}
		if err = sender.AddRTPTransportSink(0, rtpSink); err != nil {
			return err
		}
		if err = sender.AddRTPSourceStreamGst(0, source, gstSCReAM); err != nil {
			return err
		}

		rtcpSink, err := transport.NewSendFlow(uint64(flags.RTCPSendPort), false)
		if err != nil {
			return err
		}
		if err = sender.SendRTCPForStream(0, rtcpSink); err != nil {
			return err
		}

		rtcpSrc, err := transport.NewReceiveFlow(uint64(flags.RTCPRecvPort), false)
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
