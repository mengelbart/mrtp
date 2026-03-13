package subcmd

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/mengelbart/mrtp/cmdmain"
	"github.com/mengelbart/mrtp/data"
	"github.com/mengelbart/mrtp/flags"
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

type SendGo struct{}

// Exec implements cmdmain.SubCmd.
func (s *SendGo) Exec(cmd string, args []string) error {
	fs := flag.NewFlagSet("send-go", flag.ExitOnError)

	flags.RegisterInto(fs, []flags.FlagName{
		flags.LocalAddrFlag,
		flags.RemoteAddrFlag,
		flags.RTPPortFlag,
		flags.RTPFlowIDFlag,
		flags.RoQMappingFlag,
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
		flags.DataChannelFileFlag,
		flags.DataChannelStartDelayFlag,
		flags.DataChannelChunkFlag,
		flags.SourceLocationFlag,
		flags.RoQServerFlag,
		flags.RoQClientFlag,
		flags.CodecFlag,
	}...)

	fs.IntVar(&UDPRecvBufferSize, "recv-buffer-size", UDPRecvBufferSize, "UDP receive 'buffer-size' of Gstreamer udpsrc element")

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

	if flags.QuicCC > 2 {
		fmt.Fprintf(os.Stderr, "Invalid %v value, must be 0, 1 or 2.\n", flags.QuicCCFlag)
		fs.Usage()
		os.Exit(1)
	}

	if flags.QuicPacer > 1 {
		fmt.Fprintf(os.Stderr, "Invalid %v value, must be 0 or 1.\n", flags.QuicPacerFlag)
		fs.Usage()
		os.Exit(1)
	}

	if flags.RoQMapping > 2 {
		fmt.Fprintf(os.Stderr, "Invalid %v value, must be 0, 1 or 2.\n", flags.RoQMappingFlag)
		fs.Usage()
		os.Exit(1)
	}

	if flags.QuicPacer == 1 && (!flags.CCnada && !flags.CCgcc) {
		fmt.Fprintf(os.Stderr, "Flag -%v can only be used with NADA or GCC\n", flags.QuicPacerFlag)
		fs.Usage()
		os.Exit(1)
	}

	if flags.DataChannel && (flags.QuicCC == 1 || (flags.QuicCC == 2 && flags.QuicPacer != 1)) {
		fmt.Fprintf(os.Stderr, "Flag -%v only allowed if Reno as CC or rate based pacer. NoCC option allways invalid\n", flags.DataChannelFlag)
		fs.Usage()
		os.Exit(1)
	}

	if len(fs.Args()) > 1 {
		fmt.Fprintf(os.Stderr, "error: unknown extra arguments: %v\n", flag.Args()[1:])
		fs.Usage()
		os.Exit(1)
	}

	quicOptions := []quictransport.Option{
		quictransport.WithRole(quictransport.Role(flags.RoQServer)),
		quictransport.SetQuicCC(int(flags.QuicCC)),
		quictransport.SetLocalAddress(flags.LocalAddr, flags.RTPPort), // TODO: which port to use?
		quictransport.SetRemoteAddress(flags.RemoteAddr, flags.RTPPort),
		quictransport.WithPacer(int(flags.QuicPacer)),
	}

	if flags.CCnada {
		feedbackDelta := uint64(20)
		quicOptions = append(quicOptions, quictransport.EnableNADA(750_000, 250_000, flags.MaxTargetRate, uint(feedbackDelta), uint64(flags.NadaFeedbackFlowID)))
	}

	if flags.CCgcc {
		quicOptions = append(quicOptions, quictransport.EnableGCC(750_000, 250_000, int(flags.MaxTargetRate), uint64(flags.NadaFeedbackFlowID)))
	}
	if flags.LogQuic {
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
		if flowID == uint64(flags.RTPFlowID) || flowID == uint64(flags.RTCPRecvFlowID) || flowID == uint64(flags.RTCPSendFlowID) {
			roqTransport.HandleUniStreamWithFlowID(flowID, roqProtocol.NewQuicGoReceiveStream(rs))
			return
		}
		if flags.DataChannel && dcTransport != nil {
			dcTransport.ReadStream(context.Background(), rs, flowID)
			return
		}

		panic(fmt.Sprint("unknown stream flowID ", flowID))
	}
	quicConn.StartHandlers()

	// open dc connection
	var dataSource *data.DataBin
	if flags.DataChannel {
		dcSender, err := dcTransport.NewDataChannelSender(uint64(flags.DataChannelFlowID), 0, true)
		if err != nil {
			return err
		}

		dataSource, err = createDataSource(dcSender, flags.DcSourceFile, flags.DcStartDelay, false, flags.DcChunks)
		if err != nil {
			return err
		}

		go dataSource.Run(ctx)
	}

	rtpSink, err := roqTransport.NewSendFlow(uint64(flags.RTPFlowID), roq.SendMode(flags.RoQMapping), flags.TraceRTPSend)
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

	file, err := os.Open(flags.SourceLocation)
	if err != nil {
		return err
	}
	defer file.Close()

	fileSrc, err := gopipe.NewY4MSource(file)
	if err != nil {
		return err
	}

	i := fileSrc.GetInfo()
	codecTyp, err := codec.CodecTypeFromString(flags.Codec)
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
