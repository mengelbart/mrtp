package subcmd

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/mengelbart/mrtp/cmdmain"
	"github.com/mengelbart/mrtp/data"
	"github.com/mengelbart/mrtp/datachannels"
	"github.com/mengelbart/mrtp/flags"
	"github.com/mengelbart/mrtp/quictransport"
	"github.com/mengelbart/mrtp/quicutils"
	"github.com/quic-go/quic-go"
)

var (
	sourceFile string
	rateLimit  uint
)

func init() {
	cmdmain.RegisterSubCmd("send-data", func() cmdmain.SubCmd { return new(SendData) })
}

// SendData is a command to run a receiver pipeline for data channels.
type SendData struct{}

func (s *SendData) Help() string {
	return "Run sender pipeline for data channels"
}

func (s *SendData) Exec(cmd string, args []string) error {
	fs := flag.NewFlagSet("send-data", flag.ExitOnError)
	flags.RegisterInto(fs, []flags.FlagName{
		flags.LocalAddrFlag,
		flags.RemoteAddrFlag,
		flags.QuicCCFlag,
		flags.CCnadaFlag,
		flags.CCgccFlag,
		flags.MaxTragetRateFlag,
	}...)

	fs.StringVar(&sourceFile, "source-file", "", "File to be sent. If empty, random data will be sent.")
	fs.UintVar(&rateLimit, "fixed-rate-limit", 0, "Rate limit in bits per second. 0 means no limit.")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `%v

Usage:
	%s send [flags]

Flags:
`, s.Help(), cmd)
		fs.PrintDefaults()
		fmt.Fprintln(os.Stderr)
	}
	fs.Parse(args)

	if (flags.CCnada || flags.CCgcc) && rateLimit > 0 {
		return fmt.Errorf("cannot use fixed rate limit with NADA or GCC")
	}

	quicTOptions := []quictransport.Option{
		quictransport.WithRole(quicutils.Role(quicutils.RoleClient)),
		quictransport.SetQuicCC(int(flags.QuicCC)),
		quictransport.SetLocalAdress(flags.LocalAddr, 8080),
		quictransport.SetRemoteAdress(flags.RemoteAddr, 8080),
	}

	if flags.CCnada {
		feedbackDelta := uint64(20)
		quicTOptions = append(quicTOptions, quictransport.EnableNADA(750_000, 150_000, flags.MaxTargetRate, uint(feedbackDelta)))
	}

	if flags.CCgcc {
		quicTOptions = append(quicTOptions, quictransport.EnableGCC(750_000, 150_000, int(flags.MaxTargetRate)))
	}

	// open quic connection
	quicConn, err := quictransport.New([]string{roqALPN}, quicTOptions...)
	if err != nil {
		return err
	}
	dcTransport := quicConn.GetQuicDataChannel()

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
	quicConn.StartHandlers()

	// blocks until we get OpenChannelOk
	sender, err := dcTransport.NewDataChannelSender(42, 0)
	if err != nil {
		return err
	}

	source, err := createDataSource(sender)
	if err != nil {
		return err
	}

	go source.Run()

	if flags.CCgcc || flags.CCnada {
		// rate is controlled by cc
		quicConn.SetSourceTargetRate = func(ratebps uint) error {
			source.SetRateLimit(ratebps)
			return nil
		}
	} else if rateLimit > 0 {
		// fixed rate limit
		source.SetRateLimit(rateLimit)
	}

	select {}
}

func createDataSource(sender *datachannels.Sender) (*data.DataBin, error) {
	sourceOptions := []data.DataBinOption{
		data.DataBinUseRateLimiter(750_000, 10000), // burst not relevant, as data source sends small chunks anyways
	}
	if sourceFile != "" {
		// check if file exists
		if _, err := os.Stat(sourceFile); errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("file does not exist: %v", sourceFile)
		}
		sourceOptions = append(sourceOptions, data.DataBinUseFileSource(sourceFile))
	}

	return data.NewDataBin(sender, sourceOptions...)

}
