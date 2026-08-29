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

	"github.com/mengelbart/mrtp/cmdmain"
	"github.com/mengelbart/mrtp/data"
	"github.com/mengelbart/mrtp/datachannels"
	"github.com/mengelbart/mrtp/internal/quictransport"
	"github.com/quic-go/quic-go"
)

func init() {
	cmdmain.RegisterSubCmd("send-data", func() cmdmain.SubCmd { return new(SendData) })
}

// SendData is a command to run a receiver pipeline for data channels.
type SendData struct {
	localAddr         string
	remoteAddr        string
	maxTargetRate     uint
	dataChannelFlowID uint
	bwe               string
}

func (s *SendData) Help() string {
	return "Run sender pipeline for data channels"
}

func (s *SendData) Exec(cmd string, args []string) error {
	fs := flag.NewFlagSet("send-data", flag.ExitOnError)
	fs.StringVar(&s.localAddr, "local", "127.0.0.1", "Local address")
	fs.StringVar(&s.remoteAddr, "remote", "127.0.0.1", "Remote address")
	fs.StringVar(&s.bwe, "bwe", "", "Set a bandwidth estimator by name, e.g. 'nada' or 'gcc'")
	fs.UintVar(&s.maxTargetRate, "max-target-rate", 3_000_000, "Set the maximum target rate of the congestion controller in bits per second")
	fs.UintVar(&s.dataChannelFlowID, "dc-flow-id", 3, "Data Channel Flow ID when using quic data channels")

	sourceFile := fs.String("source-file", "", "File to be sent. If empty, random data will be sent.")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `%v

Usage:
	%s send-data [flags]

Flags:
`, s.Help(), cmd)
		fs.PrintDefaults()
		fmt.Fprintln(os.Stderr)
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	if len(s.bwe) == 0 {
		return fmt.Errorf("bwe has to be set")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	quicOptions := []quictransport.Option{
		quictransport.WithRole(quictransport.Role(quictransport.RoleClient)),
		quictransport.SetLocalAddress(s.localAddr, 8080),
		quictransport.SetRemoteAddress(s.remoteAddr, 8080),
		quictransport.SetQLOGLabel("sender"),
	}

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

	// open quic connection
	quicConn, err := quictransport.New(ctx, []string{roqALPN}, quicOptions...)
	if err != nil {
		return err
	}

	dcTransport, err := datachannels.New(ctx, quicConn.GetQuicConnection())
	if err != nil {
		return err
	}

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
	quicConn.StartHandlers()

	// blocks until we get OpenChannelOk
	sender, err := dcTransport.NewDataChannelSender(ctx, uint64(s.dataChannelFlowID), 0, true)
	if err != nil {
		return err
	}

	source, err := createDataSource(sender, *sourceFile, 0, true, false)
	if err != nil {
		return err
	}

	go func() {
		if sourceErr := source.Run(ctx); sourceErr != nil {
			panic(sourceErr)
		}
	}()

	quicConn.SetSourceTargetRate = func(ratebps uint) error {
		// log "combined" target rate even if we do not split it. Makes plotting easier
		slog.Info("NEW_TARGET_RATE", "rate", ratebps)

		source.SetRateLimit(ratebps)
		return nil
	}

	<-ctx.Done()
	return ctx.Err()
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
