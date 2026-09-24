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
	"strings"
	"time"

	"github.com/mengelbart/mrtp"
	"github.com/mengelbart/mrtp/cmdmain"
	"github.com/mengelbart/mrtp/element/fake"
	"github.com/mengelbart/mrtp/element/rtp"
	"github.com/mengelbart/mrtp/pipeline"
	"github.com/mengelbart/mrtp/signaling"
	"github.com/mengelbart/mrtp/udp"
	"github.com/mengelbart/mrtp/webrtc"
)

func init() {
	cmdmain.RegisterSubCmd("client", func() cmdmain.SubCmd { return new(Client) })
}

// webrtcConnectTimeout bounds gathering ICE candidates and establishing the
// peer connection.
const webrtcConnectTimeout = 30 * time.Second

type Client struct {
	serverURL     string
	protocol      string
	duration      time.Duration
	bitrate       uint
	fps           uint64
	mtu           uint
	traceRTP      bool
	bwe           string
	pacing        bool
	maxTargetRate uint
}

// Help implements cmdmain.SubCmd.
func (c *Client) Help() string {
	return "Open a session on a signaling server and send media"
}

func (c *Client) Exec(cmd string, args []string) error {
	fs := flag.NewFlagSet("client", flag.ExitOnError)
	fs.StringVar(&c.serverURL, "server", "http://127.0.0.1:8080", "Signaling server URL")
	fs.StringVar(&c.protocol, "protocol", signaling.ProtocolRTPUDP, "Media transport, 'rtp-udp' or 'webrtc'")
	fs.DurationVar(&c.duration, "duration", 10*time.Second, "How long to send")
	fs.UintVar(&c.bitrate, "bitrate", 1_000_000, "Media bitrate in bits per second, excluding RTP headers")
	fs.Uint64Var(&c.fps, "fps", 30, "Frames per second")
	fs.UintVar(&c.mtu, "mtu", 1200, "Maximum RTP packet size in bytes")
	fs.BoolVar(&c.traceRTP, "trace-rtp-send", false, "Log outgoing RTP packets")
	fs.StringVar(&c.bwe, "bwe", "", "Set a bandwidth estimator by name for webrtc, e.g. 'nada', 'gcc' or 'scream'")
	fs.BoolVar(&c.pacing, "pacing", false, "Enable packet pacing for webrtc")
	fs.UintVar(&c.maxTargetRate, "max-target-rate", 30_000_000, "Maximum target rate of the congestion controller in bits per second")
	DefaultBweFlags.ConfigureFlags(fs)

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Open a session on a signaling server and send fake video as RTP over UDP or WebRTC

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

	if err := c.validate(fs); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	bounds := mrtp.RateBounds{Initial: c.bitrate, Min: c.bitrate, Max: c.bitrate}
	if c.bwe != "" {
		bounds = mrtp.RateBounds{Initial: c.bitrate, Min: minTargetRate, Max: c.maxTargetRate}
	}
	source, err := fake.New(c.duration, c.fps, bounds)
	if err != nil {
		return err
	}
	format, err := mrtp.NewRTPFormat(mrtp.Fake, mrtp.DefaultPayloadType)
	if err != nil {
		return err
	}
	packetizer := rtp.NewPacketizer(uint16(c.mtu), format.PayloadType, rand.Uint32(), format.ClockRate, format.Codec)

	signaler := &signaling.Client{BaseURL: c.serverURL}
	if c.protocol == signaling.ProtocolWebRTC {
		return c.sendWebRTC(ctx, signaler, source, packetizer)
	}
	return c.sendRTPUDP(ctx, signaler, source, packetizer)
}

// validate checks flag combinations.
func (c *Client) validate(fs *flag.FlagSet) error {
	if c.mtu > math.MaxUint16 {
		return fmt.Errorf("invalid -mtu value %v", c.mtu)
	}
	switch c.protocol {
	case signaling.ProtocolRTPUDP:
		var webrtcOnly []string
		fs.Visit(func(f *flag.Flag) {
			switch f.Name {
			case "bwe", "pacing":
				webrtcOnly = append(webrtcOnly, "-"+f.Name)
			}
		})
		if len(webrtcOnly) > 0 {
			return fmt.Errorf("%v only apply to -protocol %v", strings.Join(webrtcOnly, ", "), signaling.ProtocolWebRTC)
		}
		return nil
	case signaling.ProtocolWebRTC:
	default:
		return fmt.Errorf("unknown protocol %q", c.protocol)
	}
	if c.pacing && c.bwe == "scream" {
		return errors.New("-pacing cannot be combined with -bwe scream")
	}
	return nil
}

func (c *Client) sendRTPUDP(ctx context.Context, signaler *signaling.Client, source *fake.Source, packetizer *rtp.Packetizer) error {
	session, err := signaler.Open(ctx, signaling.Request{Protocol: signaling.ProtocolRTPUDP})
	if err != nil {
		return err
	}
	if session.RTP == nil {
		closeSession(signaler, session.ID)
		return errors.New("session response has no RTP endpoint")
	}
	slog.Info("opened session", "id", session.ID, "rtp", session.RTP.Address)
	defer closeSession(signaler, session.ID)

	sink, err := udp.Dial(session.RTP.Address, c.traceRTP, func(p *mrtp.RTPPacket) *[]byte { return &p.Data })
	if err != nil {
		return err
	}
	return sendMedia(ctx, pipeline.NewGraph(), source, packetizer, sink)
}

func (c *Client) sendWebRTC(ctx context.Context, signaler *signaling.Client, source *fake.Source, packetizer *rtp.Packetizer) error {
	stdnet, err := webrtc.NewNet(webrtc.SetRecvBufferSize(10_000_000))
	if err != nil {
		return err
	}
	setupCtx, cancelSetup := context.WithTimeout(ctx, webrtcConnectTimeout)
	defer cancelSetup()
	connectedCtx, cancelConnected := context.WithCancel(setupCtx)
	defer cancelConnected()

	options := []webrtc.Option{
		webrtc.SetNet(stdnet),
		webrtc.SetSRTPBufferLimit(10_000_000),
		webrtc.RegisterDefaultCodecs(),
		webrtc.RegisterFakeCodec(),
		webrtc.EnableCCFBReceiver(),
		webrtc.EnableNACK(),
		webrtc.EnableRTCPReports(),
		webrtc.SetICEServers(nil),
		webrtc.IncludeLoopbackCandidates(),
		webrtc.OnConnected(cancelConnected),
	}
	if c.traceRTP {
		options = append(options, webrtc.EnableRTPSendTraceLogging())
	}
	if c.pacing {
		options = append(options, webrtc.EnablePacing())
	}
	if c.bwe != "" {
		bweOptions, bweErr := makeWebRTCBWE(c.bwe, BWEConfig{
			InitTargetRate: c.bitrate,
			MinTargetRate:  minTargetRate,
			MaxTargetRate:  c.maxTargetRate,
		})
		if bweErr != nil {
			return bweErr
		}
		options = append(options, bweOptions...)
	}
	transport, err := webrtc.NewTransport(nil, true, options...)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := transport.Close(); closeErr != nil {
			slog.Error("failed to close WebRTC transport", "error", closeErr)
		}
	}()
	track, err := transport.AddLocalTrackWithCodec(mrtp.Fake.MimeType())
	if err != nil {
		return err
	}
	// Reading RTCP drives the interceptors and the congestion controller.
	rtcp := track.RTCPReceiver()
	defer rtcp.Close()

	offer, err := transport.Offer(setupCtx)
	if err != nil {
		return err
	}
	session, err := signaler.Open(ctx, signaling.Request{
		Protocol: signaling.ProtocolWebRTC,
		WebRTC:   &signaling.WebRTCOffer{SDP: offer},
	})
	if err != nil {
		return err
	}
	slog.Info("opened session", "id", session.ID, "protocol", signaling.ProtocolWebRTC)
	defer closeSession(signaler, session.ID)
	if session.WebRTC == nil {
		return errors.New("session response has no WebRTC answer")
	}
	if err = transport.SetAnswer(session.WebRTC.SDP); err != nil {
		return err
	}

	<-connectedCtx.Done()
	if err = setupCtx.Err(); err != nil {
		return fmt.Errorf("peer connection not established within %v: %w", webrtcConnectTimeout, err)
	}
	transport.ControlBitrate(source)

	g := pipeline.NewGraph()
	pump := pipeline.NewPump[mrtp.RTCPPacket]()
	if err = errors.Join(g.Attach(rtcp, pump), g.Connect(pump, pipeline.NewDiscard[mrtp.RTCPPacket]())); err != nil {
		_ = g.Close()
		return err
	}
	return sendMedia(ctx, g, source, packetizer, track)
}

// closeSession closes the session on the server even if the run context is
// cancelled already.
func closeSession(signaler *signaling.Client, id string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := signaler.Close(ctx, id); err != nil {
		slog.Error("failed to close session", "id", id, "error", err)
	}
}

// sendMedia adds source, packetizer and sink to g and runs it until the source
// ends.
func sendMedia(ctx context.Context, g *pipeline.Graph, source *fake.Source, packetizer *rtp.Packetizer, sink mrtp.Sink[mrtp.RTPPacket]) error {
	defer func() {
		if closeErr := g.Close(); closeErr != nil {
			slog.Error("failed to close pipeline", "error", closeErr)
		}
	}()
	if err := errors.Join(g.Connect(source, packetizer), g.Connect(packetizer, sink)); err != nil {
		_ = sink.Close()
		return err
	}
	g.Terminal(source)

	if err := g.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}
