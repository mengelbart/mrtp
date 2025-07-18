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
	"github.com/mengelbart/mrtp/logging"
	"github.com/mengelbart/mrtp/roq"
)

func init() {
	cmdmain.RegisterSubCmd("receive", func() cmdmain.SubCmd {
		return new(Receive)
	})
}

var MakeStreamSink = func(name string) (gstreamer.RTPSinkBin, error) {
	return gstreamer.NewStreamSink(
		name,
		gstreamer.StreamSinkType(gstreamer.SinkType(flags.SinkType)),
		gstreamer.StreamSinkLocation(flags.Location),
	)
}

var (
	udpSrcTraceRTP bool
)

type Receive struct {
	receiver *gstreamer.RTPBin
	sink     gstreamer.RTPSinkBin
}

func (r *Receive) Help() string {
	return "Run receiver pipeline"
}

func (r *Receive) Exec(cmd string, args []string) error {
	fs := flag.NewFlagSet("receive", flag.ExitOnError)

	// override default values
	flags.RTPPort = 5000
	flags.RTCPSendPort = 5002
	flags.RTCPRecvPort = 5001
	flags.RegisterInto(fs, []flags.FlagName{
		flags.LocalAddrFlag,
		flags.RemoteAddrFlag,
		flags.RTPPortFlag,
		flags.RTCPSendPortFlag,
		flags.RTCPRecvPortFlag,
		flags.RoQServerFlag,
		flags.RoQClientFlag,
		flags.GstCCFBFlag,
		flags.SinkTypeFlag,
		flags.LocationFlag,
		flags.LogFileFlag,
	}...)

	fs.BoolVar(&udpSrcTraceRTP, "udp-src-trace-rtp", false, "Log incoming RTP packets on UDPSrc")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Run a receiver pipeline

Usage:
	%v receive [flags]

Flags:
`, cmd)
		fs.PrintDefaults()
		fmt.Fprintln(os.Stderr)
	}
	fs.Parse(args)

	// use log file
	if flags.LogFile != "" {
		f, err := os.Create(flags.LogFile)
		if err != nil {
			panic(err)
		}
		defer f.Close()

		logging.UseFileForLogging(f)
	}

	if len(fs.Args()) > 1 {
		fmt.Printf("error: unknown extra arguments: %v\n", flag.Args()[1:])
		fs.Usage()
		os.Exit(1)
	}

	if flags.SinkType == uint(gstreamer.Filesink) && len(flags.Location) == 0 {
		return errors.New("file-sink requires a location to be set via the -location flag")
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

	var err error
	r.receiver, err = gstreamer.NewRTPBin()
	if err != nil {
		return err
	}

	r.sink, err = MakeStreamSink("rtp-stream-sink")
	if err != nil {
		return err
	}

	if flags.RoQServer || flags.RoQClient {
		err = r.setupRoQ()
	} else {
		err = r.setupUDP()
	}
	if err != nil {
		return err
	}
	return r.receiver.Run()
}

func (r *Receive) setupRoQ() error {
	transport, err := roq.New(
		roq.WithRole(roq.Role(flags.RoQServer)),
	)
	if err != nil {
		return err
	}

	rtpSrc, err := transport.NewReceiveFlow(uint64(flags.RTPPort))
	if err != nil {
		return err
	}
	if err = r.receiver.AddRTPSink(0, r.sink); err != nil {
		return err
	}
	if err = r.receiver.ReceiveRTPStreamFrom(0, rtpSrc, flags.GstCCFB); err != nil {
		return err
	}

	rtcpSink, err := transport.NewSendFlow(uint64(flags.RTCPSendPort))
	if err != nil {
		return err
	}
	if err = r.receiver.SendRTCPForStream(0, rtcpSink); err != nil {
		return err
	}

	rtcpSrc, err := transport.NewReceiveFlow(uint64(flags.RTCPRecvPort))
	if err != nil {
		return err
	}
	if err = r.receiver.ReceiveRTCPFrom(rtcpSrc); err != nil {
		return err
	}
	return nil
}

func (r *Receive) setupUDP() error {
	rtpSrc, err := gstreamer.NewUDPSrc(flags.LocalAddr, uint32(flags.RTPPort), gstreamer.EnabelUDPSrcPadProbe(udpSrcTraceRTP))
	if err != nil {
		return err
	}
	if err = r.receiver.AddRTPSink(0, r.sink); err != nil {
		return err
	}
	if err = r.receiver.ReceiveRTPStreamFromGst(0, rtpSrc.GetGstElement(), flags.GstCCFB); err != nil {
		return err
	}

	rtcpSink, err := gstreamer.NewUDPSink(flags.RemoteAddr, uint32(flags.RTCPSendPort))
	if err != nil {
		return err
	}
	if err = r.receiver.SendRTCPForStreamGst(0, rtcpSink.GetGstElement()); err != nil {
		return err
	}

	rtcpSrc, err := gstreamer.NewUDPSrc(flags.LocalAddr, uint32(flags.RTCPRecvPort))
	if err != nil {
		return err
	}
	if err = r.receiver.ReceiveRTCPFromGst(rtcpSrc.GetGstElement()); err != nil {
		return err
	}
	return nil
}
