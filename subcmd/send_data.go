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
	"github.com/mengelbart/mrtp/datachannels"
	"github.com/mengelbart/mrtp/internal/quictransport"
	"github.com/mengelbart/mrtp/pipeline"
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

	sourceFile := fs.String("source-file", "", "File to be sent. If empty, synthetic data will be sent.")

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

	source, err := createDataSource(*sourceFile, 0, mrtp.RateBounds{
		Initial: 750_000,
		Min:     minTargetRate,
		Max:     s.maxTargetRate,
	}, false)
	if err != nil {
		return err
	}

	runner := pipeline.NewRunner()
	defer func() {
		if closeErr := runner.Close(); closeErr != nil {
			slog.Error("failed to close pipelines", "error", closeErr)
		}
	}()

	graph := pipeline.NewGraph()
	if err = graph.Connect(source, sender); err != nil {
		return err
	}
	runner.Add(graph)

	quicConn.SetSourceTargetRate = func(ratebps uint) error {
		// log "combined" target rate even if we do not split it. Makes plotting easier
		slog.Info("NEW_TARGET_RATE", "rate", ratebps)

		return source.SetTargetBitrate(ratebps)
	}

	return runner.Run(ctx)
}

// dataSource is a source of data channel traffic.
type dataSource interface {
	mrtp.Source[mrtp.DataChunk]
	mrtp.Driver
	mrtp.TargetBitrateSetter
	Running() bool
}

// createDataSource returns a source that sends sourceFile, or synthetic data
// if sourceFile is empty, paced to bounds.
func createDataSource(sourceFile string, startDelaySeconds uint, bounds mrtp.RateBounds, chunkSource bool) (dataSource, error) {
	startDelay := time.Duration(startDelaySeconds) * time.Second
	if sourceFile != "" {
		return data.NewFileSource(sourceFile, bounds, startDelay)
	}
	config := data.StreamConfig()
	if chunkSource {
		config = data.ChunkConfig()
	}
	config.Bounds = bounds
	config.StartDelay = startDelay
	return data.NewSource(config)
}
