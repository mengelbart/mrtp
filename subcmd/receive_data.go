package subcmd

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/mengelbart/mrtp/cmdmain"
	"github.com/mengelbart/mrtp/data"
	"github.com/mengelbart/mrtp/datachannels"
	"github.com/mengelbart/mrtp/internal/quictransport"
	"github.com/quic-go/quic-go"
)

func init() {
	cmdmain.RegisterSubCmd("receive-data", func() cmdmain.SubCmd { return new(ReceiveData) })
}

// ReceiveData is a command to run a receiver pipeline for data channels.
type ReceiveData struct {
	localAddr         string
	remoteAddr        string
	dataChannelFlowID uint
}

func (r *ReceiveData) Help() string {
	return "Run receiver pipeline for data channels"
}

func (r *ReceiveData) Exec(cmd string, args []string) error {
	fs := flag.NewFlagSet("receive-data", flag.ExitOnError)
	fs.StringVar(&r.localAddr, "local", "127.0.0.1", "Local address")
	fs.StringVar(&r.remoteAddr, "remote", "127.0.0.1", "Remote address")
	fs.UintVar(&r.dataChannelFlowID, "dc-flow-id", 3, "Data Channel Flow ID when using quic data channels")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `%v

Usage:
	%v receive-data [flags]

Flags:
`, r.Help(), cmd)
		fs.PrintDefaults()
		fmt.Fprintln(os.Stderr)
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	quicOptions := []quictransport.Option{
		quictransport.WithRole(quictransport.Role(quictransport.RoleServer)),
		quictransport.SetLocalAddress(r.localAddr, 8080),
		quictransport.SetRemoteAddress(r.remoteAddr, 8080),
		quictransport.SetQLOGLabel("receiver"),
	}

	quicConn, err := quictransport.New(ctx, []string{roqALPN}, quicOptions...)
	if err != nil {
		return err
	}

	dcTransport, err := datachannels.New(ctx, quicConn.GetQuicConnection())
	if err != nil {
		return err
	}

	// start handler
	quicConn.StartHandlers()

	go func() {
		if dataErr := r.startDataChannelReceiver(ctx, dcTransport); dataErr != nil {
			slog.Error("failed to start data channel receiver", "error", dataErr)
		}
	}()

	// set handlers for datagrams and streams
	quicConn.HandleDatagram = func(flowID uint64, dgram []byte) {
		// no datagrams expected
	}

	quicConn.HandleUniStream = func(flowID uint64, rs *quic.ReceiveStream) {
		if readErr := dcTransport.ReadStream(ctx, datachannels.NewQuicGoReceiveStream(rs), flowID); readErr != nil {
			slog.Error("failed to forward stream", "flowID", flowID, "error", readErr)
			cancel()
		}
	}

	<-ctx.Done()
	return ctx.Err()
}

func (r *ReceiveData) startDataChannelReceiver(ctx context.Context, dcTransport *datachannels.Transport) error {
	receiver, err := dcTransport.AddDataChannelReceiver(ctx, uint64(r.dataChannelFlowID))
	if err != nil {
		return err
	}

	sink, err := data.NewSink(receiver)
	if err != nil {
		return err
	}

	return sink.Run()
}
