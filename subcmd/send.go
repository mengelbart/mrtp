package subcmd

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"math"
	"os"

	"github.com/mengelbart/mrtp"
	"github.com/mengelbart/mrtp/cmdmain"
	"github.com/mengelbart/mrtp/data"
	"github.com/mengelbart/mrtp/flags"
	"github.com/mengelbart/mrtp/gstreamer"
	"github.com/mengelbart/mrtp/internal/quictransport"
	"github.com/mengelbart/mrtp/roq"
	roqProtocol "github.com/mengelbart/roq"
	"github.com/quic-go/quic-go"
)

const roqALPN = "roq-09"

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
	sourceLocation string
	codec          string
}

func (f *gstreamerVideoStreamSourceFactory) ConfigureFlags(fs *flag.FlagSet) {
	fs.StringVar(&f.sourceLocation, "source-location", "", "Location for filesource (or videotestsrc to generate a testsource)")
	// check if codec flag is already registered - relevant for webrtc subcmd
	if fs.Lookup("codec") == nil {
		fs.StringVar(&f.codec, "codec", mrtp.H264.String(), "Codec to use (H264, VP8)")
	}
}

func (f *gstreamerVideoStreamSourceFactory) MakeStreamSource(name string) (gstreamer.RTPSourceBin, error) {
	codec, error := mrtp.NewCodec(f.codec)
	if error != nil {
		return nil, error
	}

	streamSourceOpts := []gstreamer.StreamSourceOption{gstreamer.StreamSourceCodec(codec)}

	if f.sourceLocation != "videotestsrc" {
		// check if file exists
		if _, err := os.Stat(f.sourceLocation); errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("file does not exist: %v", f.sourceLocation)
		}

		streamSourceOpts = append(streamSourceOpts, gstreamer.StreamSourceFileSourceLocation(f.sourceLocation))
		streamSourceOpts = append(streamSourceOpts, gstreamer.StreamSourceType(gstreamer.Filesrc))
	}
	return gstreamer.NewStreamSource(name, streamSourceOpts...)
}

var DefaultStreamSourceFactory StreamSourceFactory = &gstreamerVideoStreamSourceFactory{}

var (
	gstSCReAM   bool
	dcPercatage uint
)

type Send struct {
	localAddr         string
	remoteAddr        string
	roqMapping        uint
	roqServer         bool
	roqClient         bool
	qlog              bool
	quicPacer         uint
	quicCC            uint
	nada              bool
	gcc               bool
	maxTargetRate     uint
	traceRTP          bool
	datachannel       bool
	dcSourceFile      string
	dcStartDelay      uint
	dcChunks          bool
	feedbackFlowID    uint
	dataChannelFlowID uint
	udpPort           uint
	rtpFlowID         uint
	rtcpSendPort      uint
	rtcpRecvPort      uint
}

func (s *Send) Help() string {
	return "Run sender pipeline"
}

func (s *Send) Exec(cmd string, args []string) error {
	fs := flag.NewFlagSet("send", flag.ExitOnError)
	fs.StringVar(&s.localAddr, "local", "127.0.0.1", "Local address")
	fs.StringVar(&s.remoteAddr, "remote", "127.0.0.1", "Remote address")
	fs.UintVar(&s.roqMapping, "roq-mapping", 0, "RTP mapping to QUIC. 0: datagrams, 1: stream per packet, 2: single stream")
	fs.BoolVar(&s.roqServer, "roq-server", false, "Use RoQ server transport")
	fs.BoolVar(&s.roqClient, "roq-client", false, "Use RoQ client transport")
	fs.BoolVar(&s.qlog, "log-quic", false, "Log quic internal events")
	fs.UintVar(&s.quicPacer, "quic-pacer", 0, "Which quic pacer to use. 0: default, 1: rate based pacer")
	fs.UintVar(&s.quicCC, "quic-cc", 0, "Which quic CC to use. 0: Reno, 1: no CC and no pacer, 2: only pacer")
	fs.BoolVar(&s.nada, "nada", false, "Enable NADA congestion control")
	fs.BoolVar(&s.gcc, "pion-gcc", false, "Enable GCC congestion control")
	fs.UintVar(&s.maxTargetRate, "max-target-rate", 3_000_000, "Set the maximum target rate of the congestion controller in bits per second")
	fs.BoolVar(&s.traceRTP, "trace-rtp-send", false, "Log outgoing RTP packets")
	fs.BoolVar(&s.datachannel, "dc", false, "Send/Receive data with data channels")
	fs.StringVar(&s.dcSourceFile, "dc-source", "", "File to be sent. If empty, random data will be sent.")
	fs.UintVar(&s.dcStartDelay, "dc-start-delay", 0, "Start delay in seconds before data channel source starts sending data.")
	fs.BoolVar(&s.dcChunks, "dc-chunks", false, "Send chunks on datachannel")
	fs.UintVar(&s.feedbackFlowID, "nada-feedback-flow-id", 4, "NADA Feedback Flow ID when using NADA or GCC with QUIC")
	fs.UintVar(&s.dataChannelFlowID, "dc-flow-id", 3, "Data Channel Flow ID when using quic data channels")
	fs.UintVar(&s.udpPort, "rtp-port", 5000, "UDP Port number for outgoing RTP stream")
	fs.UintVar(&s.rtpFlowID, "rtp-flow-id", 0, "RTP Flow ID when using RTP over QUIC")
	fs.UintVar(&s.rtcpSendPort, "rtcp-send-porto", 5001, "UDP port for outgoing RTCP stream")
	fs.UintVar(&s.rtcpRecvPort, "rtcp-recv-porto", 5002, "UDP port for incoming RTCP stream")

	flags.RegisterInto(fs, []flags.FlagName{
		flags.RTCPRecvFlowIDFlag,
		flags.RTCPSendFlowIDFlag,
	}...)
	fs.BoolVar(&gstSCReAM, "gst-scream", false, "Run SCReAM Gstreamer element")
	fs.UintVar(&dcPercatage, "dc-tr-share", 30, "Percentage of target rate to be used for data channel (RoQ only)")

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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if s.quicCC > 2 {
		fmt.Fprintf(os.Stderr, "Invalid %v value, must be 0, 1 or 2.\n", s.quicCC)
		fs.Usage()
		os.Exit(1)
	}

	if s.quicPacer > 1 {
		fmt.Fprintf(os.Stderr, "Invalid %v value, must be 0 or 1.\n", s.quicPacer)
		fs.Usage()
		os.Exit(1)
	}

	if s.roqMapping > 2 {
		fmt.Fprintf(os.Stderr, "Invalid %v value, must be 0, 1 or 2.\n", s.roqMapping)
		fs.Usage()
		os.Exit(1)
	}

	if (s.nada || s.gcc || s.quicCC != 0 || s.quicPacer != 0 || s.qlog || s.roqMapping != 0) && (!s.roqServer && !s.roqClient) {
		fmt.Fprintf(os.Stderr, "Flags -%v, -%v, -%v, -%v and -%v are only valid for RoQ\n", "nada", "pion-gcc", s.quicCC, s.qlog, s.roqMapping)
		fs.Usage()
		os.Exit(1)
	}

	if s.quicPacer == 1 && (!s.nada && !s.gcc) {
		fmt.Fprintf(os.Stderr, "Flag -%v can only be used with NADA or GCC\n", s.quicPacer)
		fs.Usage()
		os.Exit(1)
	}

	if s.datachannel && (!s.roqServer && !s.roqClient) {
		fmt.Fprintf(os.Stderr, "Flag -%v only valid for RoQ\n", "dc")
		fs.Usage()
		os.Exit(1)
	}

	if s.datachannel && (s.quicCC == 1 || (s.quicCC == 2 && s.quicPacer != 1)) {
		fmt.Fprintf(os.Stderr, "Flag -%v only allowed if Reno as CC or rate based pacer. NoCC option allways invalid\n", "dc")
		fs.Usage()
		os.Exit(1)
	}

	if len(fs.Args()) > 1 {
		fmt.Fprintf(os.Stderr, "error: unknown extra arguments: %v\n", flag.Args()[1:])
		fs.Usage()
		os.Exit(1)
	}

	for _, p := range []uint{
		s.rtcpRecvPort,
		s.rtcpSendPort,
		s.udpPort,
	} {
		if p > math.MaxUint32 {
			return fmt.Errorf("invalid port number: %v", p)
		}
	}
	if s.roqClient && s.roqServer {
		return errors.New("cannot run RoQ server and client simultaneously")
	}

	source, err := DefaultStreamSourceFactory.MakeStreamSource("rtp-stream-source")
	if err != nil {
		return err
	}

	rtpBinOpts := []gstreamer.RTPBinOption{}
	if gstSCReAM {
		rtpBinOpts = append(rtpBinOpts, gstreamer.EnableSCReAM(750, 250, s.maxTargetRate/1000))
	}

	sender, err := gstreamer.NewRTPBin(rtpBinOpts...)
	if err != nil {
		return err
	}

	mediaBa, ok := source.(BitrateAdapter)
	if ok {
		sender.SetTargetRateEncoder = mediaBa.SetBitrate
	}

	if s.roqServer || s.roqClient {
		quicOptions := []quictransport.Option{
			quictransport.WithRole(quictransport.Role(s.roqServer)),
			quictransport.SetQuicCC(int(s.quicCC)),
			quictransport.SetLocalAddress(s.localAddr, s.udpPort),
			quictransport.SetRemoteAddress(s.remoteAddr, s.udpPort),
			quictransport.WithPacer(int(s.quicPacer)),
		}

		if s.nada {
			feedbackDelta := uint64(20)
			quicOptions = append(quicOptions, quictransport.EnableNADA(750_000, 250_000, s.maxTargetRate, uint(feedbackDelta), uint64(s.feedbackFlowID)))
		}

		if s.gcc {
			quicOptions = append(quicOptions, quictransport.EnableGCC(750_000, 250_000, int(s.maxTargetRate), uint64(s.feedbackFlowID)))
		}
		if s.qlog {
			quicOptions = append(quicOptions, quictransport.EnableQLogs("./sender.qlog"))
		}

		// open quic connection
		quicConn, err := quictransport.New(ctx, []string{roqALPN}, quicOptions...)
		if err != nil {
			return err
		}
		dcTransport := quicConn.GetQuicDataChannel()

		// open roq connection
		roqOpt := []roq.Option{roq.EnableRoqLogs("sender.roq.qlog")}
		roqTransport, err := roq.New(ctx, quicConn.GetQuicConnection(), roqOpt...)
		if err != nil {
			return err
		}
		defer roqTransport.CloseLogFile()

		// set handlers for datagrams and streams
		quicConn.HandleDatagram = func(flowID uint64, dgram []byte) {
			// all datagrams belong to RoQ for now
			roqTransport.HandleDatagram(dgram)
		}
		quicConn.HandleUintStream = func(flowID uint64, rs *quic.ReceiveStream) {
			if flowID == uint64(s.rtpFlowID) || flowID == uint64(flags.RTCPRecvFlowID) || flowID == uint64(flags.RTCPSendFlowID) {
				roqTransport.HandleUniStreamWithFlowID(flowID, roqProtocol.NewQuicGoReceiveStream(rs))
				return
			}
			if s.datachannel && dcTransport != nil {
				dcTransport.ReadStream(context.Background(), rs, flowID)
				return
			}

			panic(fmt.Sprint("unknown stream flowID ", flowID))
		}
		quicConn.StartHandlers()

		// open dc connection
		var dataSource *data.DataBin
		if s.datachannel {
			dcSender, err := dcTransport.NewDataChannelSender(uint64(s.dataChannelFlowID), 0, true)
			if err != nil {
				return err
			}

			dataSource, err = createDataSource(dcSender, s.dcSourceFile, s.dcStartDelay, false, s.dcChunks)
			if err != nil {
				return err
			}

			go dataSource.Run(ctx)
		}

		// set rate callbacks
		quicConn.SetSourceTargetRate = func(ratebps uint) error {
			slog.Info("NEW_TARGET_RATE", "rate", ratebps)

			mediaTargetRate := ratebps
			if s.datachannel && dataSource != nil && dataSource.Running() {
				mediaTargetRate = ratebps * (100 - dcPercatage) / 100
			}
			err := mediaBa.SetBitrate(mediaTargetRate)
			if err != nil {
				panic(err)
			}

			return nil
		}

		rtpSink, err := roqTransport.NewSendFlow(uint64(s.rtpFlowID), roq.SendMode(s.roqMapping), s.traceRTP)
		if err != nil {
			return err
		}
		if err = sender.AddRTPTransportSink(0, rtpSink); err != nil {
			return err
		}
		if err = sender.AddRTPSourceStreamGst(0, source); err != nil {
			return err
		}

		rtcpSink, err := roqTransport.NewSendFlow(uint64(flags.RTCPSendFlowID), roq.SendMode(s.roqMapping), false)
		if err != nil {
			return err
		}
		if err = sender.SendRTCPForStream(0, rtcpSink); err != nil {
			return err
		}

		rtcpSrc, err := roqTransport.NewReceiveFlow(uint64(flags.RTCPRecvFlowID), false)
		if err != nil {
			return err
		}
		if err = sender.ReceiveRTCPFrom(rtcpSrc); err != nil {
			return err
		}

	} else {
		rtpSink, err := gstreamer.NewUDPSink(s.remoteAddr, uint32(s.udpPort), gstreamer.EnabelUDPSinkPadProbe(s.traceRTP))
		if err != nil {
			return err
		}
		if err = sender.AddRTPTransportSinkGst(0, rtpSink.GetGstElement()); err != nil {
			return err
		}
		if err = sender.AddRTPSourceStreamGst(0, source); err != nil {
			return err
		}

		rtcpSink, err := gstreamer.NewUDPSink(s.remoteAddr, uint32(s.rtcpSendPort))
		if err != nil {
			return err
		}
		if err = sender.SendRTCPForStreamGst(0, rtcpSink.GetGstElement()); err != nil {
			return err
		}

		rtcpSrc, err := gstreamer.NewUDPSrc(s.localAddr, uint32(s.rtcpRecvPort))
		if err != nil {
			return err
		}
		if err = sender.ReceiveRTCPFromGst(rtcpSrc.GetGstElement()); err != nil {
			return err
		}
	}

	return sender.Run()
}
