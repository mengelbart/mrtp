package subcmd

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"math"
	"os"
	"strings"

	"github.com/mengelbart/mrtp/cmdmain"
	"github.com/mengelbart/mrtp/data"
	"github.com/mengelbart/mrtp/datachannels"
	"github.com/mengelbart/mrtp/internal/quictransport"
	"github.com/mengelbart/mrtp/media"
	"github.com/mengelbart/mrtp/roq"
	"github.com/mengelbart/mrtp/udp"
	"github.com/quic-go/quic-go"
)

func init() {
	cmdmain.RegisterSubCmd("send", func() cmdmain.SubCmd { return new(Send) })
}

var dcPercentage uint

type Send struct {
	localAddr         string
	remoteAddr        string
	roqMapping        uint
	roqServer         bool
	roqClient         bool
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
	transport         string

	media      media.Flags
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
	fs.UintVar(&dcPercentage, "dc-tr-share", 50, "Percentage of target rate to be used for data channel (RoQ only)")
	fs.StringVar(&s.transport, "transport", DefaultTransport,
		fmt.Sprintf("Transport for plain RTP, ignored when RoQ is enabled or when the media pipeline moves the packets itself (%v)", strings.Join(transportNames, ", ")))

	if err := s.media.ConfigureSender(fs); err != nil {
		return err
	}
	DefaultBweFlags.ConfigureFlags(fs)

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Run a sender pipeline

Usage:
	%s send [flags]

Flags:
`, cmd)
		fs.PrintDefaults()
		fmt.Fprintln(os.Stderr)
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if s.roqMapping > 2 {
		fmt.Fprintf(os.Stderr, "Invalid -roq-mapping value %v, must be 0, 1 or 2.\n", s.roqMapping)
		fs.Usage()
		os.Exit(1)
	}

	if (s.bwe == "nada" || s.bwe == "gcc" || s.roqMapping != 0) && (!s.roqServer && !s.roqClient) {
		fmt.Fprintf(os.Stderr, "Flags -bwe {gcc,nada}, and -roq-mapping are only valid for RoQ\n")
		fs.Usage()
		os.Exit(1)
	}

	if s.datachannel && (!s.roqServer && !s.roqClient) {
		fmt.Fprintf(os.Stderr, "Flag -%v only valid for RoQ\n", "dc")
		fs.Usage()
		os.Exit(1)
	}

	if len(fs.Args()) > 0 {
		fmt.Fprintf(os.Stderr, "error: unknown extra arguments: %v\n", fs.Args())
		fs.Usage()
		os.Exit(1)
	}

	for _, p := range []uint{
		s.rtcpRecvPort,
		s.rtcpSendPort,
		s.udpPort,
	} {
		if p > math.MaxUint16 {
			return fmt.Errorf("invalid port number: %v", p)
		}
	}
	if s.roqClient && s.roqServer {
		return errors.New("cannot run RoQ server and client simultaneously")
	}

	senderConfig, err := s.media.SenderConfig("rtp-stream-source")
	if err != nil {
		return err
	}
	senderConfig.RateBounds = media.RateBounds{
		Initial: initTargetRate,
		Min:     minTargetRate,
		Max:     s.maxTargetRate,
	}

	pipeline, err := s.media.NewPipeline()
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := pipeline.Close(); closeErr != nil {
			slog.Error("failed to close media pipeline", "error", closeErr)
		}
	}()

	if s.roqServer || s.roqClient {
		quicOptions := []quictransport.Option{
			quictransport.WithRole(quictransport.Role(s.roqServer)),
			quictransport.SetLocalAddress(s.localAddr, s.udpPort),
			quictransport.SetRemoteAddress(s.remoteAddr, s.udpPort),
			quictransport.SetQLOGLabel("sender"),
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

		// open quic connection
		quicConn, err := quictransport.New(ctx, []string{roqALPN}, quicOptions...)
		if err != nil {
			return err
		}
		defer quicConn.Close()

		// open roq connection
		roqTransport, err := roq.New(ctx, quicConn.GetQuicConnection())
		if err != nil {
			return err
		}
		defer func() {
			_ = roqTransport.Close()
		}()

		dcTransport, err := datachannels.New(ctx, quicConn.GetQuicConnection())
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
				if readErr := dcTransport.ReadStream(ctx, datachannels.NewQuicGoReceiveStream(rs), flowID); readErr != nil {
					slog.Error("failed to read stream", "error", readErr)
				}
				return
			}

			slog.Error("unknown stream flow ID, closing stream", "flow-id", flowID)
			rs.CancelRead(0)
		}
		quicConn.StartHandlers()

		// open dc connection
		// var dataSource *data.DataBin
		if s.datachannel {
			dcSender, dcErr := dcTransport.NewDataChannelSender(ctx, uint64(s.dataChannelFlowID), 0, true)
			if dcErr != nil {
				return dcErr
			}

			s.dataSource, err = createDataSource(dcSender, s.dcSourceFile, s.dcStartDelay, false, s.dcChunks)
			if err != nil {
				return err
			}

			go func() {
				if datasourceErr := s.dataSource.Run(ctx); datasourceErr != nil {
					slog.Error("failed to run data source", "error", datasourceErr)
				}
			}()
		}

		rtpSink, err := roqTransport.NewSendFlow(uint64(s.rtpFlowID), roq.SendMode(s.roqMapping), s.traceRTP)
		if err != nil {
			return err
		}
		rtcpSink, err := roqTransport.NewSendFlow(uint64(s.rtcpSendFlowID), roq.SendMode(s.roqMapping), false)
		if err != nil {
			return err
		}
		rtcpSrc, err := roqTransport.NewReceiveFlow(uint64(s.rtcpRecvFlowID), false)
		if err != nil {
			return err
		}

		senderConfig.RTP = rtpSink
		senderConfig.RTCP = media.RTCPFlow{Send: rtcpSink, Recv: rtcpSrc}
		mediaSender, err := pipeline.AddSender(senderConfig)
		if err != nil {
			return err
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
			return mediaSender.SetTargetBitrate(mediaTargetRate)
		}

	} else {
		_, err := s.setupPlainRTP(pipeline, senderConfig)
		if err != nil {
			return err
		}
	}

	return pipeline.Run(ctx)
}

func (s *Send) setupPlainRTP(pipeline media.Pipeline, config media.SenderConfig) (media.Sender, error) {
	switch s.transport {
	case transportGstUDP:
		// The pipeline sends the packets itself, from its own flags. Opening a
		// socket here would only bind the same port twice.
		return pipeline.AddSender(config)
	case transportUDP:
	default:
		return nil, fmt.Errorf("unknown transport %q, available: %v", s.transport, transportNames)
	}

	rtpSink, err := udp.Dial(address(s.remoteAddr, uint16(s.udpPort)), s.traceRTP)
	if err != nil {
		return nil, err
	}
	rtcpSink, err := udp.Dial(address(s.remoteAddr, uint16(s.rtcpSendPort)), false)
	if err != nil {
		return nil, err
	}
	rtcpSrc, err := udp.Listen(address(s.localAddr, uint16(s.rtcpRecvPort)), false)
	if err != nil {
		return nil, err
	}

	config.RTP = rtpSink
	config.RTCP = media.RTCPFlow{Send: rtcpSink, Recv: rtcpSrc}
	return pipeline.AddSender(config)
}
