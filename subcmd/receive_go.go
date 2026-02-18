package subcmd

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/mengelbart/mrtp/cmdmain"
	"github.com/mengelbart/mrtp/codec"
	"github.com/mengelbart/mrtp/data"
	"github.com/mengelbart/mrtp/flags"
	"github.com/mengelbart/mrtp/gstreamer"
	"github.com/mengelbart/mrtp/internal/quictransport"
	"github.com/mengelbart/mrtp/roq"
	roqProtocol "github.com/mengelbart/roq"
	"github.com/quic-go/quic-go"
)

func init() {
	cmdmain.RegisterSubCmd("receivego", func() cmdmain.SubCmd { return new(ReceiveGo) })
}

type ReceiveGo struct {
	receiver *gstreamer.RTPBin
	sink     gstreamer.RTPSinkBin
}

func (r *ReceiveGo) Help() string {
	return "Run receiver pipeline"
}

func (r *ReceiveGo) Exec(cmd string, args []string) error {
	fs := flag.NewFlagSet("receivego", flag.ExitOnError)

	flags.RegisterInto(fs, []flags.FlagName{
		flags.LocalAddrFlag,
		flags.RemoteAddrFlag,
		flags.RTPPortFlag,
		flags.RTPFlowIDFlag,
		flags.RoQMappingFlag,
		flags.GstCCFBFlag,
		flags.TraceRTPRecvFlag,
		flags.NadaFeedbackFlag,
		flags.DataChannelFlag,
		flags.NadaFeedbackFlowIDFlag,
		flags.DataChannelFlowIDFlag,
		flags.LogQuicFlag,
		flags.RoQServerFlag,
		flags.RoQClientFlag,
	}...)

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

	if flags.RoQMapping > 2 {
		fmt.Fprintf(os.Stderr, "Invalid %v value, must be 0, 1 or 2\n", flags.RoQMappingFlag)
		fs.Usage()
		os.Exit(1)
	}

	if flags.SinkType == uint(gstreamer.Filesink) && len(flags.SinkLocation) == 0 {
		return errors.New("file-sink requires a location to be set via the -sink-location flag")
	}

	quicOptions := []quictransport.Option{
		quictransport.WithRole(quictransport.Role(flags.RoQServer)),
		quictransport.SetLocalAddress(flags.LocalAddr, flags.RTPPort), // TODO: which port to use?
		quictransport.SetRemoteAddress(flags.RemoteAddr, flags.RTPPort),
	}

	if flags.NadaFeedback {
		feedbackDelta := time.Duration(20 * time.Millisecond)
		quicOptions = append(quicOptions, quictransport.EnableNADAfeedback(feedbackDelta, uint64(flags.NadaFeedbackFlowID)))
	}

	if flags.LogQuic {
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
		if flowID == uint64(flags.RTPFlowID) || flowID == uint64(flags.RTCPRecvFlowID) || flowID == uint64(flags.RTCPSendFlowID) {
			roqTransport.HandleUniStreamWithFlowID(flowID, roqProtocol.NewQuicGoReceiveStream(rs))
			return
		}

		if flags.DataChannel {
			dcTransport.ReadStream(context.Background(), rs, flowID)
			return
		}

		panic(fmt.Sprint("unknown stream flowID ", flowID))
	}

	// start handler
	quicConn.StartHandlers()

	if flags.DataChannel {
		// setup data channel receiver
		// quic tranpsorts has to be started before
		dcReceiver, err := dcTransport.AddDataChannelReceiver(uint64(flags.DataChannelFlowID))
		if err != nil {
			return err
		}

		dataSink, err := data.NewSink(dcReceiver)
		if err != nil {
			return err
		}

		go dataSink.Run()
	}

	rtpSrc, err := roqTransport.NewReceiveFlow(uint64(flags.RTPFlowID), flags.TraceRTPRecv)
	if err != nil {
		return err
	}

	decoder, err := codec.NewDecoder()
	if err != nil {
		return err
	}

	fileSink, err := codec.NewY4MSink("./out.y4m", 30, 1)
	if err != nil {
		return err
	}

	timeout := 100 * time.Millisecond
	depacketizer := codec.NewRTPDepacketizer(timeout)
	defer depacketizer.Close()

	rtpPipeline, err := codec.Chain(codec.Info{}, fileSink, decoder, depacketizer)
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
		err = rtpPipeline.Write(buf[:n], codec.Attributes{})
		if err != nil {
			return err
		}
	}
}
