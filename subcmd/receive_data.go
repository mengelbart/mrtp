package subcmd

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/mengelbart/mrtp/cmdmain"
	"github.com/mengelbart/mrtp/data"
	"github.com/mengelbart/mrtp/datachannels"
	"github.com/mengelbart/mrtp/flags"
	"github.com/mengelbart/mrtp/quictransport"
	"github.com/quic-go/quic-go"
)

func init() {
	cmdmain.RegisterSubCmd("receive-data", func() cmdmain.SubCmd { return new(ReceiveData) })
}

// ReceiveData is a command to run a receiver pipeline for data channels.
type ReceiveData struct {
}

func (r *ReceiveData) Help() string {
	return "Run receiver pipeline for data channels"
}

func (r *ReceiveData) Exec(cmd string, args []string) error {
	fs := flag.NewFlagSet("receive-data", flag.ExitOnError)

	flags.RegisterInto(fs, []flags.FlagName{
		flags.LocalAddrFlag,
		flags.RemoteAddrFlag,
		flags.NadaFeedbackFlag,
		flags.LogQuicFlag,
		flags.NadaFeedbackFlowIDFlag,
		flags.DataChannelFlowIDFlag,
	}...)

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `%v

Usage:
	%v receive-data [flags]

Flags:
`, r.Help(), cmd)
		fs.PrintDefaults()
		fmt.Fprintln(os.Stderr)
	}
	fs.Parse(args)

	quicOptions := []quictransport.Option{
		quictransport.WithRole(quictransport.Role(quictransport.RoleServer)),
		quictransport.SetLocalAdress(flags.LocalAddr, 8080),
		quictransport.SetRemoteAdress(flags.RemoteAddr, 8080),
	}

	if flags.NadaFeedback {
		feedbackDelta := uint64(20)
		quicOptions = append(quicOptions, quictransport.EnableNADAfeedback(feedbackDelta, uint64(flags.NadaFeedbackFlowID)))
	}

	if flags.LogQuic {
		quicOptions = append(quicOptions, quictransport.EnableQLogs("./receiver.qlog"))
	}

	quicConn, err := quictransport.New([]string{roqALPN}, quicOptions...)
	if err != nil {
		return err
	}

	dcTransport := quicConn.GetQuicDataChannel()

	// start handler
	quicConn.StartHandlers()

	go r.startDataChannelReceiver(dcTransport)

	// set handlers for datagrams and streams
	quicConn.HandleDatagram = func(flowID uint64, dgram []byte) {
		// no datagrams expected
	}

	quicConn.HandleUintStream = func(flowID uint64, rs *quic.ReceiveStream) {
		err := dcTransport.ReadStream(context.Background(), rs, flowID)
		if err != nil {
			panic(fmt.Sprintf("forward stream with flowID: %v: %v", flowID, err))
		}
	}

	select {}
}

func (r *ReceiveData) startDataChannelReceiver(dcTransport *datachannels.Transport) error {
	receiver, err := dcTransport.AddDataChannelReceiver(uint64(flags.DataChannelFlowID))
	if err != nil {
		return err
	}

	sink, err := data.NewSink(receiver)
	if err != nil {
		panic(err)
	}

	err = sink.Run()
	if err != nil {
		panic(err)
	}

	return nil
}
