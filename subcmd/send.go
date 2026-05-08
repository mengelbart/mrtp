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
	"github.com/mengelbart/mrtp/datachannels"
	"github.com/mengelbart/mrtp/gstreamer"
	"github.com/mengelbart/mrtp/internal/quictransport"
	"github.com/mengelbart/mrtp/roq"
	"github.com/quic-go/quic-go"
)

const roqALPN = "roq-09"

const (
	initTargetRate = 1_000_000
	minTargetRate  = 400_000
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
	sourceLocation string
	codec          string
}

func (f *gstreamerVideoStreamSourceFactory) ConfigureFlags(fs *flag.FlagSet) {
	fs.StringVar(&f.sourceLocation, "source-location", "", "Location for filesource (or videotestsrc to generate a testsource)")
	fs.StringVar(&f.codec, "source-codec", mrtp.H264.String(), "Codec to use for encoder (H264, VP8)")
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
	gstSCReAM    bool
	dcPercentage uint
)

type Send struct {
	localAddr         string
	remoteAddr        string
	roqMapping        uint
	roqServer         bool
	roqClient         bool
	qlog              bool
	bwe               string
	maxTargetRate     uint
	traceRTP          bool
	datachannel       bool
	dcSourceFile      string
	dcStartDelay      uint
	dcChunks          bool
	dataChannelFlowID uint
	udpPort           uint
	rtcpSendPort      uint
	rtcpRecvPort      uint
	rtpFlowID         uint
	rtcpSendFlowID    uint
	rtcpRecvFlowID    uint

	dataSource *data.DataBin
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
	fs.StringVar(&s.bwe, "bwe", "", "Set a bandwidth estimator by name, e.g. 'nada' or 'gcc'")
	fs.UintVar(&s.maxTargetRate, "max-target-rate", 30_000_000, "Set the maximum target rate of the congestion controller in bits per second")
	fs.BoolVar(&s.traceRTP, "trace-rtp-send", false, "Log outgoing RTP packets")
	fs.BoolVar(&s.datachannel, "dc", false, "Send/Receive data with data channels")
	fs.StringVar(&s.dcSourceFile, "dc-source", "", "File to be sent. If empty, random data will be sent.")
	fs.UintVar(&s.dcStartDelay, "dc-start-delay", 0, "Start delay in seconds before data channel source starts sending data.")
	fs.BoolVar(&s.dcChunks, "dc-chunks", false, "Send chunks on datachannel")
	fs.UintVar(&s.dataChannelFlowID, "dc-flow-id", 3, "Data Channel Flow ID when using quic data channels")
	fs.UintVar(&s.udpPort, "rtp-port", 5000, "UDP Port number for outgoing RTP stream")
	fs.UintVar(&s.rtpFlowID, "rtp-flow-id", 0, "RTP Flow ID when using RTP over QUIC")
	fs.UintVar(&s.rtcpSendPort, "rtcp-send-porto", 5001, "UDP port for outgoing RTCP stream")
	fs.UintVar(&s.rtcpRecvPort, "rtcp-recv-porto", 5002, "UDP port for incoming RTCP stream")
	fs.UintVar(&s.rtcpSendFlowID, "rtcp-send-flow-id", 2, "RTCP Sender Flow ID when using RTP over QUIC")
	fs.UintVar(&s.rtcpRecvFlowID, "rtcp-recv-flow-id", 1, "RTCP Receiver Flow ID when using RTP over QUIC")
	fs.BoolVar(&gstSCReAM, "gst-scream", false, "Run SCReAM Gstreamer element")
	fs.UintVar(&dcPercentage, "dc-tr-share", 50, "Percentage of target rate to be used for data channel (RoQ only)")

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

	if s.roqMapping > 2 {
		fmt.Fprintf(os.Stderr, "Invalid -roq-mapping value %v, must be 0, 1 or 2.\n", s.roqMapping)
		fs.Usage()
		os.Exit(1)
	}

	if (s.bwe == "nada" || s.bwe == "gcc" || s.qlog || s.roqMapping != 0) && (!s.roqServer && !s.roqClient) {
		fmt.Fprintf(os.Stderr, "Flags -bwe {gcc,nada}, -log-quic and -roq-mapping are only valid for RoQ\n")
		fs.Usage()
		os.Exit(1)
	}

	if s.datachannel && (!s.roqServer && !s.roqClient) {
		fmt.Fprintf(os.Stderr, "Flag -%v only valid for RoQ\n", "dc")
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
		rtpBinOpts = append(rtpBinOpts, gstreamer.EnableSCReAM(initTargetRate/1000, minTargetRate/1000, s.maxTargetRate/1000))
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
			quictransport.SetLocalAddress(s.localAddr, s.udpPort),
			quictransport.SetRemoteAddress(s.remoteAddr, s.udpPort),
			quictransport.PacingFactor(s.pacingFactor),
		}

		if len(s.bwe) > 0 {
			bweFactory, ok := BWEFactories[s.bwe]
			if !ok {
				return fmt.Errorf("unknown BWE: %v", s.bwe)
			}
			bwe, err := bweFactory.MakeBWE(BWEConfig{
				InitTargetRate: initTargetRate,
				MinTargetRate:  minTargetRate,
				MaxTargetRate:  s.maxTargetRate,
			})
			if err != nil {
				return err
			}
			quicOptions = append(quicOptions, quictransport.SetBWE(bwe))
		}

		if s.qlog {
			quicOptions = append(quicOptions, quictransport.EnableQLogs("sender"))
		}

		// open quic connection
		quicConn, err := quictransport.New(ctx, []string{roqALPN}, quicOptions...)
		if err != nil {
			return err
		}

		// open roq connection
		roqOpt := []roq.Option{roq.EnableRoqLogs("sender.roq.qlog")}
		roqTransport, err := roq.New(ctx, quicConn.GetQuicConnection(), roqOpt...)
		if err != nil {
			return err
		}
		defer roqTransport.CloseLogFile()

		dcTransport, err := datachannels.New(quicConn.GetQuicConnection())
		if err != nil {
			return err
		}

		// set handlers for datagrams and streams
		quicConn.HandleDatagram = func(flowID uint64, dgram []byte) {
			// all datagrams belong to RoQ for now
			roqTransport.HandleDatagram(dgram)
		}
		quicConn.HandleUniStream = func(flowID uint64, rs *quic.ReceiveStream) {
			if flowID == uint64(s.rtpFlowID) || flowID == uint64(s.rtcpRecvFlowID) || flowID == uint64(s.rtcpSendFlowID) {
				roqTransport.HandleUniStreamWithFlowID(flowID, roq.NewQuicGoReceiveStream(rs))
				return
			}
			if s.datachannel && dcTransport != nil {
				dcTransport.ReadStream(context.Background(), datachannels.NewQuicGoReceiveStream(rs), flowID)
				return
			}

			panic(fmt.Sprint("unknown stream flowID ", flowID))
		}
		quicConn.StartHandlers()

		// open dc connection
		// var dataSource *data.DataBin
		if s.datachannel {
			dcSender, err := dcTransport.NewDataChannelSender(uint64(s.dataChannelFlowID), 0, true)
			if err != nil {
				return err
			}

			s.dataSource, err = createDataSource(dcSender, s.dcSourceFile, s.dcStartDelay, false, s.dcChunks)
			if err != nil {
				return err
			}

			go s.dataSource.Run(ctx)
		}

		// set rate callbacks
		quicConn.SetSourceTargetRate = func(ratebps uint) error {
			slog.Info("NEW_TARGET_RATE", "rate", ratebps)

			var mediaTargetRate uint
			if s.datachannel && s.dataSource != nil && s.dataSource.Running() {
				mediaTargetRate = ratebps * (100 - dcPercentage) / 100
			} else {
				mediaTargetRate = uint(0.8 * float64(ratebps))
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

		rtcpSink, err := roqTransport.NewSendFlow(uint64(s.rtcpSendFlowID), roq.SendMode(s.roqMapping), false)
		if err != nil {
			return err
		}
		if err = sender.SendRTCPForStream(0, rtcpSink); err != nil {
			return err
		}

		rtcpSrc, err := roqTransport.NewReceiveFlow(uint64(s.rtcpRecvFlowID), false)
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

func (s *Send) pacingFactor() float64 {
	if s.dataSource != nil && s.dataSource.Running() {
		// slog.Info("pacing factor", "factor", 1.0, "s.dataSource", s.dataSource, "s.dataSource.Running()", s.dataSource.Running())
		return 1.0
	}
	// slog.Info("pacing factor", "factor", 1.5, "s.dataSource", s.dataSource, "s.dataSource.Running()", s.dataSource.Running())
	return 1.0
}
