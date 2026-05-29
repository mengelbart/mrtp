package subcmd

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/mengelbart/mrtp"
	"github.com/mengelbart/mrtp/cmdmain"
	"github.com/mengelbart/mrtp/data"
	"github.com/mengelbart/mrtp/datachannels"
	"github.com/mengelbart/mrtp/internal/quictransport"
	"github.com/quic-go/quic-go"
)

var (
	rateLimit uint
)

func init() {
	cmdmain.RegisterSubCmd("send-data", func() cmdmain.SubCmd { return new(SendData) })
}

// SendData is a command to run a receiver pipeline for data channels.
type SendData struct {
	localAddr         string
	remoteAddr        string
	nada              bool
	gcc               bool
	maxTargetRate     uint
	dataChannelFlowID uint
}

func (s *SendData) Help() string {
	return "Run sender pipeline for data channels"
}

func (s *SendData) Exec(cmd string, args []string) error {
	fs := flag.NewFlagSet("send-data", flag.ExitOnError)
	fs.StringVar(&s.localAddr, "local", "127.0.0.1", "Local address")
	fs.StringVar(&s.remoteAddr, "remote", "127.0.0.1", "Remote address")
	fs.BoolVar(&s.nada, "nada", false, "Enable NADA congestion control")
	fs.BoolVar(&s.gcc, "pion-gcc", false, "Enable GCC congestion control")
	fs.UintVar(&s.maxTargetRate, "max-target-rate", 3_000_000, "Set the maximum target rate of the congestion controller in bits per second")
	fs.UintVar(&s.dataChannelFlowID, "dc-flow-id", 3, "Data Channel Flow ID when using quic data channels")

	sourceFile := fs.String("source-file", "", "File to be sent. If empty, random data will be sent.")
	fs.UintVar(&rateLimit, "fixed-rate-limit", 0, "Rate limit in bits per second. 0 means no limit.")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `%v

Usage:
	%s send-data [flags]

Flags:
`, s.Help(), cmd)
		fs.PrintDefaults()
		fmt.Fprintln(os.Stderr)
	}
	fs.Parse(args)

	if (s.nada || s.gcc) && rateLimit > 0 {
		return fmt.Errorf("cannot use fixed rate limit with NADA or GCC")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	quicOptions := []quictransport.Option{
		quictransport.WithRole(quictransport.Role(quictransport.RoleClient)),
		quictransport.SetLocalAddress(s.localAddr, 8080),
		quictransport.SetRemoteAddress(s.remoteAddr, 8080),
		quictransport.SetQLOGLabel("sender"),
	}

	if s.nada {
		nada := mrtp.NewNada(initTargetRate, minTargetRate, s.maxTargetRate, 20*time.Millisecond)
		quicOptions = append(quicOptions, quictransport.SetBWE(nada))
	}

	if s.gcc {
		gcc, err := mrtp.NewGCC(initTargetRate, minTargetRate, s.maxTargetRate)
		if err != nil {
			return err
		}
		quicOptions = append(quicOptions, quictransport.SetBWE(gcc))
	}

	// open quic connection
	quicConn, err := quictransport.New(ctx, []string{roqALPN}, quicOptions...)
	if err != nil {
		return err
	}

	dcTransport, err := datachannels.New(quicConn.GetQuicConnection())
	if err != nil {
		return err
	}

	// set handlers for datagrams and streams
	quicConn.HandleDatagram = func(flowID uint64, dgram []byte) {
		// no datagrams expected
	}
	quicConn.HandleUniStream = func(flowID uint64, rs *quic.ReceiveStream) {
		err := dcTransport.ReadStream(context.Background(), datachannels.NewQuicGoReceiveStream(rs), flowID)
		if err != nil {
			panic(fmt.Sprintf("forward stream with flowID: %v: %v", flowID, err))
		}
	}
	quicConn.StartHandlers()

	// blocks until we get OpenChannelOk
	sender, err := dcTransport.NewDataChannelSender(uint64(s.dataChannelFlowID), 0, true)
	if err != nil {
		return err
	}

	source, err := createDataSource(sender, *sourceFile, 0, true, false)
	if err != nil {
		return err
	}

	go source.Run(ctx)

	if s.gcc || s.nada {
		// rate is controlled by cc
		quicConn.SetSourceTargetRate = func(ratebps uint) error {
			// log "combined" target rate even if we do not split it. Makes plotting easier
			slog.Info("NEW_TARGET_RATE", "rate", ratebps)

			source.SetRateLimit(ratebps)
			return nil
		}
	} else if rateLimit > 0 {
		// fixed rate limit
		source.SetRateLimit(rateLimit)
	}

	select {}
}

func createDataSource(sender io.WriteCloser, sourceFile string, startDelaySeconds uint, rateLimited bool, chunkSource bool) (*data.DataBin, error) {
	sourceOptions := []data.DataBinOption{}

	if rateLimited {
		sourceOptions = append(sourceOptions, data.UseRateLimiter(750_000, 10000)) // burst not relevant, as data source sends small chunks anyways
	}

	if startDelaySeconds > 0 {
		sourceOptions = append(sourceOptions, data.SetStartDelay(time.Duration(startDelaySeconds)*time.Second))
	}

	if sourceFile != "" {
		// check if file exists
		if _, err := os.Stat(sourceFile); errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("file does not exist: %v", sourceFile)
		}
		sourceOptions = append(sourceOptions, data.UseFileSource(sourceFile))
	}

	if chunkSource {
		sourceOptions = append(sourceOptions, data.UseChunkSource())
	}

	return data.NewDataBin(sender, sourceOptions...)
}
