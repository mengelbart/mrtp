package subcmd

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/mengelbart/mrtp"
	"github.com/mengelbart/mrtp/cmdmain"
	"github.com/mengelbart/mrtp/data"
	"github.com/mengelbart/mrtp/gopipe"
	"github.com/mengelbart/mrtp/gopipe/codec"
	"github.com/mengelbart/mrtp/gstreamer"
	"github.com/mengelbart/mrtp/internal/quictransport"
	"github.com/mengelbart/mrtp/roq"
	roqProtocol "github.com/mengelbart/roq"
	"github.com/quic-go/quic-go"
)

func init() {
	cmdmain.RegisterSubCmd("receive-go", func() cmdmain.SubCmd { return new(ReceiveGo) })
}

type ReceiveGo struct {
	localAddr         string
	remoteAddr        string
	roqServer         bool
	codec             string
	qlog              bool
	nadaFeedback      bool
	traceRTP          bool
	datachannel       bool
	feedbackFlowID    uint
	dataChannelFlowID uint
	udpPort           uint
	rtpFlowID         uint
	rtcpSendFlowID    uint
	rtcpRecvFlowID    uint

	receiver *gstreamer.RTPBin
	sink     gstreamer.RTPSinkBin
}

func (r *ReceiveGo) Help() string {
	return "Run receiver pipeline without gstreamer (experimental)"
}

func (r *ReceiveGo) Exec(cmd string, args []string) error {
	fs := flag.NewFlagSet("receive-go", flag.ExitOnError)
	fs.StringVar(&r.localAddr, "local", "127.0.0.1", "Local address")
	fs.StringVar(&r.remoteAddr, "remote", "127.0.0.1", "Remote address")
	fs.BoolVar(&r.roqServer, "roq-server", false, "Use RoQ server transport.")
	fs.StringVar(&r.codec, "codec", mrtp.H264.String(), "Codec to use (H264, VP8)")
	fs.BoolVar(&r.qlog, "log-quic", false, "Log quic internal events")
	fs.BoolVar(&r.nadaFeedback, "nada-feedback", false, "Send NADA feedback")
	fs.BoolVar(&r.traceRTP, "trace-rtp-recv", false, "Log incoming RTP packets")
	fs.BoolVar(&r.datachannel, "dc", false, "Send/Receive data with data channels")
	fs.UintVar(&r.feedbackFlowID, "nada-feedback-flow-id", 4, "QUIC Flow ID to use for sending RTCP feedback (NADA or GCC) or receiving it in case of NADA feedback")
	fs.UintVar(&r.feedbackFlowID, "feedback-flow-id", 4, "Deprecated alias for -nada-feedback-flow-id")
	fs.UintVar(&r.dataChannelFlowID, "dc-flow-id", 3, "QUIC Flow ID to use for sending/receiving data with data channels")
	fs.UintVar(&r.udpPort, "rtp-port", 5000, "UDP Port number for outgoing RTP stream")
	fs.UintVar(&r.rtpFlowID, "rtp-flow-id", 0, "RTP Flow ID when using RTP over QUIC")
	fs.UintVar(&r.rtcpSendFlowID, "rtcp-send-flow-id", 1, "RTCP Sender Flow ID when using RTP over QUIC")
	fs.UintVar(&r.rtcpRecvFlowID, "rtcp-recv-flow-id", 2, "RTCP Receiver Flow ID when using RTP over QUIC")

	fs.IntVar(&UDPRecvBufferSize, "recv-buffer-size", UDPRecvBufferSize, "UDP receive 'buffer-size' of Gstreamer udpsrc element")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Run a receiver pipeline

Usage:
	%v receive3 [flags]

Flags:
`, cmd)
		fs.PrintDefaults()
		fmt.Fprintln(os.Stderr)
	}
	fs.Parse(args)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if len(fs.Args()) > 1 {
		fmt.Fprintf(os.Stderr, "error: unknown extra arguments: %v\n", flag.Args()[1:])
		fs.Usage()
		os.Exit(1)
	}

	quicOptions := []quictransport.Option{
		quictransport.WithRole(quictransport.Role(r.roqServer)),
		quictransport.SetLocalAddress(r.localAddr, r.udpPort),
		quictransport.SetRemoteAddress(r.remoteAddr, r.udpPort),
	}

	if r.nadaFeedback {
		feedbackDelta := time.Duration(20 * time.Millisecond)
		quicOptions = append(quicOptions, quictransport.EnableNADAfeedback(feedbackDelta, uint64(r.feedbackFlowID)))
	}

	if r.qlog {
		quicOptions = append(quicOptions, quictransport.EnableQLogs("./receiver.qlog"))
	}

	quicConn, err := quictransport.New(ctx, []string{roqALPN}, quicOptions...)
	if err != nil {
		return err
	}

	roqTransport, err := roq.New(ctx, quicConn.GetQuicConnection())
	if err != nil {
		return err
	}

	dcTransport := quicConn.GetQuicDataChannel()

	// set handlers for datagrams and streams
	// have to forward it ether to roq or dc
	quicConn.HandleDatagram = func(flowID uint64, dgram []byte) {
		roqTransport.HandleDatagram(dgram)
	}
	quicConn.HandleUintStream = func(flowID uint64, rs *quic.ReceiveStream) {
		if flowID == uint64(r.rtpFlowID) || flowID == uint64(r.rtcpRecvFlowID) || flowID == uint64(r.rtcpSendFlowID) {
			roqTransport.HandleUniStreamWithFlowID(flowID, roqProtocol.NewQuicGoReceiveStream(rs))
			return
		}

		if r.datachannel {
			dcTransport.ReadStream(context.Background(), rs, flowID)
			return
		}

		panic(fmt.Sprint("unknown stream flowID ", flowID))
	}

	// start handler
	quicConn.StartHandlers()

	if r.datachannel {
		// setup data channel receiver
		// quic transports has to be started before
		dcReceiver, err := dcTransport.AddDataChannelReceiver(uint64(r.dataChannelFlowID))
		if err != nil {
			return err
		}

		dataSink, err := data.NewSink(dcReceiver)
		if err != nil {
			return err
		}

		go dataSink.Run()
	}

	rtpSrc, err := roqTransport.NewReceiveFlow(uint64(r.rtpFlowID), r.traceRTP)
	if err != nil {
		return err
	}

	codecTyp, err := codec.CodecTypeFromString(r.codec)
	if err != nil {
		return err
	}

	decoder, err := gopipe.NewDecoder(codecTyp)
	if err != nil {
		return err
	}

	fileSink, err := gopipe.NewY4MSink("./out.y4m", 30, 1)
	if err != nil {
		return err
	}

	maxTimeout := 150 * time.Millisecond
	depacketizer, err := gopipe.NewRTPDepacketizer(maxTimeout, codecTyp)
	if err != nil {
		return err
	}
	defer depacketizer.Close()

	rtpPipeline, err := gopipe.Chain(gopipe.Info{}, fileSink, decoder, depacketizer)
	if err != nil {
		return err
	}

	buf := make([]byte, 150000)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		n, err := rtpSrc.Read(buf)
		if err != nil {
			return err
		}

		depacketizer.UpdateRTT(quicConn.GetRTT())

		err = rtpPipeline.Write(buf[:n], gopipe.Attributes{})
		if err != nil {
			return err
		}
	}
}
