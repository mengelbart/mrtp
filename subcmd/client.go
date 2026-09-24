package subcmd

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"math"
	"math/rand/v2"
	"os"
	"os/signal"
	"time"

	"github.com/mengelbart/mrtp"
	"github.com/mengelbart/mrtp/cmdmain"
	"github.com/mengelbart/mrtp/fake"
	"github.com/mengelbart/mrtp/media"
	"github.com/mengelbart/mrtp/packetization"
	"github.com/mengelbart/mrtp/pipeline"
	"github.com/mengelbart/mrtp/signaling"
	"github.com/mengelbart/mrtp/udp"
)

func init() {
	cmdmain.RegisterSubCmd("client", func() cmdmain.SubCmd { return new(Client) })
}

type Client struct {
	serverURL string
	duration  time.Duration
	bitrate   uint
	fps       uint64
	mtu       uint
	traceRTP  bool
}

// Help implements cmdmain.SubCmd.
func (c *Client) Help() string {
	return "Open a session on a signaling server and send media"
}

func (c *Client) Exec(cmd string, args []string) error {
	fs := flag.NewFlagSet("client", flag.ExitOnError)
	fs.StringVar(&c.serverURL, "server", "http://127.0.0.1:8080", "Signaling server URL")
	fs.DurationVar(&c.duration, "duration", 10*time.Second, "How long to send")
	fs.UintVar(&c.bitrate, "bitrate", 1_000_000, "Media bitrate in bits per second, excluding RTP headers")
	fs.Uint64Var(&c.fps, "fps", 30, "Frames per second")
	fs.UintVar(&c.mtu, "mtu", 1200, "Maximum RTP packet size in bytes")
	fs.BoolVar(&c.traceRTP, "trace-rtp-send", false, "Log outgoing RTP packets")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Open a session on a signaling server and send fake video as RTP over UDP

Usage:
	%s client [flags]

Flags:
`, cmd)
		fs.PrintDefaults()
		fmt.Fprintln(os.Stderr)
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	if len(fs.Args()) > 0 {
		fmt.Fprintf(os.Stderr, "error: unknown extra arguments: %v\n", fs.Args())
		fs.Usage()
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if c.mtu > math.MaxUint16 {
		return fmt.Errorf("invalid -mtu value %v", c.mtu)
	}
	source, err := fake.New(c.duration, c.fps, media.RateBounds{Initial: c.bitrate, Min: c.bitrate, Max: c.bitrate})
	if err != nil {
		return err
	}
	format, err := media.RTPFormat(mrtp.Fake, media.DefaultPayloadType)
	if err != nil {
		return err
	}
	packetizer := packetization.NewRTPPacketizer(uint16(c.mtu), format.PayloadType, rand.Uint32(), format.ClockRate, format.Codec)

	signaler := &signaling.Client{BaseURL: c.serverURL}
	session, err := signaler.Open(ctx, signaling.Request{Protocol: signaling.ProtocolRTPUDP})
	if err != nil {
		return err
	}
	slog.Info("opened session", "id", session.ID, "rtp", session.RTP.Address)
	defer func() {
		// The run context may be cancelled already, closing still has to reach
		// the server.
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if closeErr := signaler.Close(closeCtx, session.ID); closeErr != nil {
			slog.Error("failed to close session", "id", session.ID, "error", closeErr)
		}
	}()

	sink, err := udp.Dial(session.RTP.Address, c.traceRTP, func(p *mrtp.RTPPacket) *[]byte { return &p.Data })
	if err != nil {
		return err
	}

	g := pipeline.NewGraph()
	defer func() {
		if closeErr := g.Close(); closeErr != nil {
			slog.Error("failed to close pipeline", "error", closeErr)
		}
	}()
	if err = errors.Join(g.Connect(source, packetizer), g.Connect(packetizer, sink)); err != nil {
		_ = sink.Close()
		return err
	}
	g.Terminal(source)

	if err = g.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}
