package subcmd

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/mengelbart/mrtp"
	"github.com/mengelbart/mrtp/cmdmain"
	"github.com/mengelbart/mrtp/data"
	"github.com/mengelbart/mrtp/gopipe"
	"github.com/mengelbart/mrtp/gopipe/codec"
	"github.com/mengelbart/mrtp/internal/quictransport"
	"github.com/mengelbart/mrtp/roq"
	roqProtocol "github.com/mengelbart/roq"
	"github.com/quic-go/quic-go"
)

func init() {
	cmdmain.RegisterSubCmd("send-go", func() cmdmain.SubCmd { return new(SendGo) })
}

// Help implements cmdmain.SubCmd.
func (s *SendGo) Help() string {
	return "Run sender pipeline without gstreamer (experimental)"
}

type SendGo struct {
	localAddr         string
	remoteAddr        string
	roqMapping        uint
	roqServer         bool
	sourceLocation    string
	codec             string
	qlog              bool
	nada              bool
	gcc               bool
	maxTargetRate     uint
	traceRTP          bool
	datachannel       bool
	dcSourceFile      string
	dcStartDelay      uint
	dcChunks          bool
	dataChannelFlowID uint
	udpPort           uint
	rtpFlowID         uint
	rtcpSendFlowID    uint
	rtcpRecvFlowID    uint
}

// Exec implements cmdmain.SubCmd.
func (s *SendGo) Exec(cmd string, args []string) error {
	fs := flag.NewFlagSet("send-go", flag.ExitOnError)
	fs.StringVar(&s.localAddr, "local", "127.0.0.1", "Local address")
	fs.StringVar(&s.remoteAddr, "remote", "127.0.0.1", "Remote address")
	fs.UintVar(&s.roqMapping, "roq-mapping", 0, "RTP mapping to QUIC. 0: datagrams, 1: stream per packet, 2: single stream")
	fs.BoolVar(&s.roqServer, "roq-server", false, "Usr RoQ server transport")
	fs.StringVar(&s.sourceLocation, "source-location", "", "Location for filesource")
	fs.StringVar(&s.codec, "codec", mrtp.H264.String(), "Codec to use (H264, VP8)")
	fs.BoolVar(&s.qlog, "log-quic", false, "Log quic internal events")
	fs.BoolVar(&s.nada, "nada", false, "Enable NADA congestion control")
	fs.BoolVar(&s.gcc, "pion-gcc", false, "Enable GCC congestion control")
	fs.UintVar(&s.maxTargetRate, "max-target-rate", 3_000_000, "Set the maximum target rate of the congestion controller in bits per second")
	fs.BoolVar(&s.traceRTP, "trace-rtp-send", false, "Log outgoing RTP packets")
	fs.BoolVar(&s.datachannel, "dc", false, "Send/Receive data with data channels")
	fs.StringVar(&s.dcSourceFile, "dc-source", "", "File to be sent. If empty, random data will be sent.")
	fs.UintVar(&s.dcStartDelay, "dc-start-delay", 0, "Start delay in seconds before data channel source starts sending data.")
	fs.BoolVar(&s.dcChunks, "dc-chunks", false, "Send chunks on datachannel")
	fs.UintVar(&s.dataChannelFlowID, "dc-flow-id", 3, "Data Channel Flow ID when using quic data channels")
	fs.UintVar(&s.udpPort, "rtp-port", 5000, "UDP Port number for outgoing RTP stream")
	fs.UintVar(&s.rtpFlowID, "rtp-flow-id", 0, "RTP Flow ID when using RTP over QUIC")
	fs.IntVar(&UDPRecvBufferSize, "recv-buffer-size", UDPRecvBufferSize, "UDP receive 'buffer-size' of Gstreamer udpsrc element")
	fs.UintVar(&s.rtcpSendFlowID, "rtcp-send-flow-id", 2, "RTCP Sender Flow ID when using RTP over QUIC")
	fs.UintVar(&s.rtcpRecvFlowID, "rtcp-recv-flow-id", 1, "RTCP Receiver Flow ID when using RTP over QUIC")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Run a sender

Usage:
	%s send3 [flags]

Flags:
`, cmd)
		fs.PrintDefaults()
		fmt.Fprintln(os.Stderr)
	}
	fs.Parse(args)

	ctx := context.Background()

	if s.roqMapping > 2 {
		fmt.Fprintf(os.Stderr, "Invalid -roq-mapping value %v, must be 0, 1 or 2.\n", s.roqMapping)
		fs.Usage()
		os.Exit(1)
	}

	if len(fs.Args()) > 1 {
		fmt.Fprintf(os.Stderr, "error: unknown extra arguments: %v\n", flag.Args()[1:])
		fs.Usage()
		os.Exit(1)
	}

	quicOptions := []quictransport.Option{
		quictransport.WithRole(quictransport.Role(s.roqServer)),
		quictransport.SetLocalAddress(s.localAddr, s.udpPort),
		quictransport.SetRemoteAddress(s.remoteAddr, s.udpPort),
	}

	if s.nada {
		feedbackDelta := uint64(20)
		quicOptions = append(quicOptions, quictransport.EnableNADA(750_000, 250_000, s.maxTargetRate, uint(feedbackDelta)))
	}

	if s.gcc {
		quicOptions = append(quicOptions, quictransport.EnableGCC(750_000, 250_000, int(s.maxTargetRate)))
	}
	if s.qlog {
		quicOptions = append(quicOptions, quictransport.EnableQLogs("./sender.qlog"))
	}

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

	// set handlers for datagrams and streams
	quicConn.HandleDatagram = func(flowID uint64, dgram []byte) {
		// all datagrams belong to RoQ for now
		roqTransport.HandleDatagram(dgram)
	}
	quicConn.HandleUintStream = func(flowID uint64, rs *quic.ReceiveStream) {
		if flowID == uint64(s.rtpFlowID) || flowID == uint64(s.rtcpRecvFlowID) || flowID == uint64(s.rtcpSendFlowID) {
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

	rtpSink, err := roqTransport.NewSendFlow(uint64(s.rtpFlowID), roq.SendMode(s.roqMapping), s.traceRTP)
	if err != nil {
		return err
	}

	defer func() {
		println("closing sender")

		// give pacer time to send everything
		time.Sleep(5 * time.Second)
		rtpSink.Close()
		roqTransport.Close()
		roqTransport.CloseLogFile()
	}()

	appSink := gopipe.WriterFunc(func(b []byte, _ gopipe.Attributes) error {
		_, err := rtpSink.Write(b)
		return err
	})

	file, err := os.Open(s.sourceLocation)
	if err != nil {
		return err
	}
	defer file.Close()

	fileSrc, err := gopipe.NewY4MSource(file)
	if err != nil {
		return err
	}

	i := fileSrc.GetInfo()
	codecTyp, err := codec.CodecTypeFromString(s.codec)
	if err != nil {
		return err
	}

	encoder := gopipe.NewEncoder(codecTyp)

	// set rate callbacks
	quicConn.SetSourceTargetRate = func(ratebps uint) error {
		slog.Info("NEW_TARGET_RATE", "rate", ratebps)

		encoder.SetTargetRate(uint64(ratebps))

		return nil
	}

	packetizer := &gopipe.RTPPacketizerFactory{
		MTU:       1420,
		PT:        96,
		SSRC:      0,
		ClockRate: 90_000,
		Codec:     codecTyp,
	}
	pacer := &gopipe.FrameSpacer{
		Ctx: ctx,
	}
	rtpPipeline, err := gopipe.Chain(i, appSink, pacer, packetizer, encoder)
	if err != nil {
		return err
	}

	time.Sleep(100 * time.Millisecond)

	return fileSrc.StartLive(ctx, rtpPipeline)
}
