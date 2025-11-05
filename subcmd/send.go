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
	"github.com/mengelbart/mrtp/quictransport"
	"github.com/mengelbart/mrtp/quicutils"
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
}

func (f *gstreamerVideoStreamSourceFactory) ConfigureFlags(fs *flag.FlagSet) {
	newFlags := []flags.FlagName{flags.SourceLocationFlag}

	// check if codec flag is already registered - relevant for webrtc subcmd
	if fs.Lookup(string(flags.CodecFlag)) == nil {
		newFlags = append(newFlags, flags.CodecFlag)
	}
	flags.RegisterInto(fs, newFlags...)
}

func (f *gstreamerVideoStreamSourceFactory) MakeStreamSource(name string) (gstreamer.RTPSourceBin, error) {
	codec, error := mrtp.NewCodec(flags.Codec)
	if error != nil {
		return nil, error
	}

	streamSourceOpts := []gstreamer.StreamSourceOption{gstreamer.StreamSourceCodec(codec)}

	if flags.SourceLocation != "videotestsrc" {
		// check if file exists
		if _, err := os.Stat(flags.SourceLocation); errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("file does not exist: %v", flags.SourceLocation)
		}

		streamSourceOpts = append(streamSourceOpts, gstreamer.StreamSourceFileSourceLocation(flags.SourceLocation))
		streamSourceOpts = append(streamSourceOpts, gstreamer.StreamSourceType(gstreamer.Filesrc))
	}
	return gstreamer.NewStreamSource(name, streamSourceOpts...)
}

var DefaultStreamSourceFactory StreamSourceFactory = &gstreamerVideoStreamSourceFactory{}

var (
	gstSCReAM   bool
	dcPercatage uint
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
		flags.RTPFlowIDFlag,
		flags.RTCPRecvFlowIDFlag,
		flags.RTCPSendFlowIDFlag,
		flags.RoQServerFlag,
		flags.RoQClientFlag,
		flags.TraceRTPSendFlag,
		flags.CCgccFlag,
		flags.CCnadaFlag,
		flags.MaxTragetRateFlag,
		flags.QuicCCFlag,
		flags.QuicPacerFlag,
		flags.LogQuicFlag,
		flags.DataChannelFlag,
		flags.NadaFeedbackFlowIDFlag,
		flags.DataChannelFlowIDFlag,
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

	if flags.QuicCC > 2 {
		fmt.Printf("error: invalid quic-cc value, must be 0, 1 or 2\n")
		fs.Usage()
		os.Exit(1)
	}

	if flags.QuicPacer > 1 {
		fmt.Printf("error: invalid quic-pacer value, must be 0 or 1\n")
		fs.Usage()
		os.Exit(1)
	}

	if (flags.CCnada || flags.CCgcc || flags.QuicCC != 0 || flags.QuicPacer != 0 || flags.LogQuic) && !(flags.RoQServer || flags.RoQClient) {
		fmt.Printf("Flags -%v, -%v, -%v and -%v are only valid for RoQ\n", flags.CCnadaFlag, flags.CCgccFlag, flags.QuicCCFlag, flags.LogQuicFlag)
		fs.Usage()
		os.Exit(1)
	}

	if flags.QuicPacer == 1 && !(flags.CCnada || flags.CCgcc) {
		fmt.Printf("Flag -%v can only be used with NADA or GCC\n", flags.QuicPacerFlag)
		fs.Usage()
		os.Exit(1)
	}

	if flags.DataChannel && !(flags.RoQServer || flags.RoQClient) {
		fmt.Printf("Flag -%v only valid for RoQ\n", flags.DataChannelFlag)
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

	rtpBinOpts := []gstreamer.RTPBinOption{}
	if gstSCReAM {
		rtpBinOpts = append(rtpBinOpts, gstreamer.EnableSCReAM(750, 150, flags.MaxTargetRate/1000))
	}

	sender, err := gstreamer.NewRTPBin(rtpBinOpts...)
	if err != nil {
		return err
	}

	mediaBa, ok := source.(BitrateAdapter)
	if ok {
		sender.SetTargetRateEncoder = mediaBa.SetBitrate
	}

	if flags.RoQServer || flags.RoQClient {
		quicOptions := []quictransport.Option{
			quictransport.WithRole(quicutils.Role(flags.RoQServer)),
			quictransport.SetQuicCC(int(flags.QuicCC)),
			quictransport.SetLocalAdress(flags.LocalAddr, flags.RTPPort), // TODO: which port to use?
			quictransport.SetRemoteAdress(flags.RemoteAddr, flags.RTPPort),
			quictransport.WithPacer(int(flags.QuicPacer)),
		}

		initRate := 750_000 * (100 - dcPercatage) / 100
		if flags.CCnada {
			feedbackDelta := uint64(20)
			quicOptions = append(quicOptions, quictransport.EnableNADA(initRate, 150_000, flags.MaxTargetRate, uint(feedbackDelta), uint64(flags.NadaFeedbackFlowID)))
		}

		if flags.CCgcc {
			quicOptions = append(quicOptions, quictransport.EnableGCC(int(initRate), 150_000, int(flags.MaxTargetRate), uint64(flags.NadaFeedbackFlowID)))
		}
		if flags.LogQuic {
			qlogWriter, err := os.Create("./sender.qlog")
			if err != nil {
				return err
			}
			quicOptions = append(quicOptions, quictransport.EnableQLogs(qlogWriter))
		}

		// open quic connection
		quicConn, err := quictransport.New([]string{roqALPN}, quicOptions...)
		if err != nil {
			return err
		}
		dcTransport := quicConn.GetQuicDataChannel()

		// open roq connection
		roqTransport, err := roq.New(quicConn.GetQuicConnection())
		if err != nil {
			return err
		}

		// set handlers for datagrams and streams
		quicConn.HandleDatagram = func(flowID uint64, dgram []byte) {
			// all datagrams belong to RoQ for now
			roqTransport.HandleDatagram(dgram)
		}
		quicConn.HandleUintStream = func(flowID uint64, rs *quic.ReceiveStream) {
			if flowID == uint64(flags.RTCPSendPort) || flowID == uint64(flags.RTPPort) {
				roqTransport.HandleUniStreamWithFlowID(flowID, roqProtocol.NewQuicGoReceiveStream(rs))
				return
			}
			if flags.DataChannel && dcTransport != nil {
				dcTransport.ReadStream(context.Background(), rs, flowID)
				return
			}

			panic(fmt.Sprint("unknown stream", flowID))
		}
		quicConn.StartHandlers()

		// open dc connection
		var dataSource *data.DataBin
		if flags.DataChannel {
			dcSender, err := dcTransport.NewDataChannelSender(uint64(flags.DataChannelFlowID), 0, true)
			if err != nil {
				return err
			}

			initDataRate := 750_000 * (dcPercatage) / 100
			sourceOptions := []data.DataBinOption{
				data.DataBinUseRateLimiter(initDataRate, 10000), // burst not relevant, as data source sends small chunks anyways
			}

			dataSource, err = data.NewDataBin(dcSender, sourceOptions...)
			if err != nil {
				return err
			}

			go dataSource.Run()
		}

		// set rate callbacks
		quicConn.SetSourceTargetRate = func(ratebps uint) error {
			slog.Info("NEW_TARGET_RATE", "rate", ratebps)

			mediaTargetRate := ratebps
			if flags.DataChannel {
				mediaTargetRate = ratebps * (100 - dcPercatage) / 100
			}
			err := mediaBa.SetBitrate(mediaTargetRate)
			if err != nil {
				panic(err)
			}

			if flags.DataChannel && dataSource != nil {
				dataRate := ratebps * dcPercatage / 100
				dataSource.SetRateLimit(dataRate)
			}

			return nil
		}

		rtpSink, err := roqTransport.NewSendFlow(uint64(flags.RTPFlowID), flags.TraceRTPSend)
		if err != nil {
			return err
		}
		if err = sender.AddRTPTransportSink(0, rtpSink); err != nil {
			return err
		}
		if err = sender.AddRTPSourceStreamGst(0, source); err != nil {
			return err
		}

		rtcpSink, err := roqTransport.NewSendFlow(uint64(flags.RTCPSendFlowID), false)
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
		rtpSink, err := gstreamer.NewUDPSink(flags.RemoteAddr, uint32(flags.RTPPort), gstreamer.EnabelUDPSinkPadProbe(flags.TraceRTPSend))
		if err != nil {
			return err
		}
		if err = sender.AddRTPTransportSinkGst(0, rtpSink.GetGstElement()); err != nil {
			return err
		}
		if err = sender.AddRTPSourceStreamGst(0, source); err != nil {
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
