package subcmd

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"math"
	"net"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"time"

	"github.com/mengelbart/mrtp"
	"github.com/mengelbart/mrtp/cmdmain"
	"github.com/mengelbart/mrtp/internal/fakemedia"
	"github.com/mengelbart/mrtp/pipeline"
	"github.com/mengelbart/mrtp/signaling"
	"github.com/mengelbart/mrtp/udp"
	"github.com/mengelbart/mrtp/webrtc"
	"github.com/mengelbart/mrtp/webrtc/ecnnet"
	pionwebrtc "github.com/pion/webrtc/v4"
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
	direction     string
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
	return "Open a session on a signaling server and send or receive media"
}

func (c *Client) Exec(cmd string, args []string) error {
	fs := flag.NewFlagSet("client", flag.ExitOnError)
	fs.StringVar(&c.serverURL, "server", "http://127.0.0.1:8080", "Signaling server URL")
	fs.StringVar(&c.protocol, "protocol", signaling.ProtocolRTPUDP, "Media transport, 'rtp-udp' or 'webrtc'")
	fs.StringVar(&c.direction, "direction", signaling.DirectionRecv, "Media direction from the client's view, 'send' or 'recv'")
	fs.DurationVar(&c.duration, "duration", 10*time.Second, "How long to send or receive, 0 for no limit")
	fs.UintVar(&c.bitrate, "bitrate", 1_000_000, "Media bitrate in bits per second, excluding RTP headers")
	fs.Uint64Var(&c.fps, "fps", 30, "Frames per second")
	fs.UintVar(&c.mtu, "mtu", 1200, "Maximum RTP packet size in bytes")
	fs.BoolVar(&c.traceRTP, "trace-rtp-send", false, "Log outgoing RTP packets")
	fs.StringVar(&c.bwe, "bwe", "", "Set a bandwidth estimator by name for webrtc, e.g. 'nada', 'gcc' or 'scream'")
	fs.BoolVar(&c.pacing, "pacing", false, "Enable packet pacing for webrtc")
	fs.UintVar(&c.maxTargetRate, "max-target-rate", 30_000_000, "Maximum target rate of the congestion controller in bits per second")
	DefaultBweFlags.ConfigureFlags(fs)

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Open a session on a signaling server and send or receive fake video as RTP over UDP or WebRTC

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

	signaler := &signaling.Client{BaseURL: c.serverURL}
	switch {
	case c.protocol == signaling.ProtocolWebRTC:
		return c.runWebRTC(ctx, signaler)
	case c.direction == signaling.DirectionSend:
		return c.sendRTPUDP(ctx, signaler)
	default:
		return c.recvRTPUDP(ctx, signaler)
	}
}

// validate checks flag combinations.
func (c *Client) validate(fs *flag.FlagSet) error {
	if c.mtu > math.MaxUint16 {
		return fmt.Errorf("invalid -mtu value %v", c.mtu)
	}
	switch c.direction {
	case signaling.DirectionSend:
	case signaling.DirectionRecv:
		if set := setFlags(fs, "bitrate", "fps", "mtu", "trace-rtp-send", "bwe", "pacing", "max-target-rate"); len(set) > 0 {
			return fmt.Errorf("%v only apply to -direction %v", strings.Join(set, ", "), signaling.DirectionSend)
		}
	default:
		return fmt.Errorf("unknown direction %q", c.direction)
	}
	switch c.protocol {
	case signaling.ProtocolRTPUDP:
		if set := setFlags(fs, "bwe", "pacing"); len(set) > 0 {
			return fmt.Errorf("%v only apply to -protocol %v", strings.Join(set, ", "), signaling.ProtocolWebRTC)
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

func setFlags(fs *flag.FlagSet, names ...string) []string {
	var set []string
	fs.Visit(func(f *flag.Flag) {
		for _, name := range names {
			if f.Name == name {
				set = append(set, "-"+name)
			}
		}
	})
	return set
}

func (c *Client) senderConfig() fakemedia.SenderConfig {
	bounds := mrtp.RateBounds{Initial: c.bitrate, Min: c.bitrate, Max: c.bitrate}
	if c.bwe != "" {
		bounds = mrtp.RateBounds{Initial: c.bitrate, Min: minTargetRate, Max: c.maxTargetRate}
	}
	return fakemedia.SenderConfig{
		Duration: c.duration,
		FPS:      c.fps,
		MTU:      uint16(c.mtu),
		Bounds:   bounds,
	}
}

// receiveContext bounds ctx by -duration.
func (c *Client) receiveContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if c.duration == 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, c.duration)
}

func (c *Client) sendRTPUDP(ctx context.Context, signaler *signaling.Client) error {
	session, err := c.openRTPUDP(ctx, signaler, "")
	if err != nil {
		return err
	}
	defer closeSession(signaler, session.ID)

	sink, err := udp.Dial(session.RTP.Address, c.traceRTP, mrtp.RTPBytes)
	if err != nil {
		return err
	}
	g := pipeline.NewGraph()
	if _, err = fakemedia.AddSender(g, c.senderConfig(), sink); err != nil {
		return errors.Join(err, g.Close())
	}
	return runGraph(ctx, g)
}

func (c *Client) recvRTPUDP(ctx context.Context, signaler *signaling.Client) error {
	ip, err := localIP(c.serverURL)
	if err != nil {
		return err
	}
	format, err := mrtp.NewRTPFormat(fakemedia.Codec, mrtp.DefaultPayloadType)
	if err != nil {
		return err
	}
	src, err := udp.Listen(net.JoinHostPort(ip.String(), "0"), false, format, mrtp.RTPBytes)
	if err != nil {
		return err
	}
	g := pipeline.NewGraph()
	discard, err := fakemedia.AddReceiver(g, src)
	if err != nil {
		return errors.Join(err, g.Close())
	}
	g.Terminal(src)

	session, err := c.openRTPUDP(ctx, signaler, src.LocalAddr().String())
	if err != nil {
		return errors.Join(err, g.Close())
	}
	defer closeSession(signaler, session.ID)

	ctx, cancel := c.receiveContext(ctx)
	defer cancel()
	// Closing the socket unblocks the pending Read.
	defer context.AfterFunc(ctx, func() { _ = src.Close() })()
	err = runGraph(ctx, g)
	slog.Info("received", "frames", discard.Packets())
	return err
}

// openRTPUDP opens an RTP over UDP session. address is where the client
// receives, empty if it sends.
func (c *Client) openRTPUDP(ctx context.Context, signaler *signaling.Client, address string) (signaling.Response, error) {
	session, err := signaler.Open(ctx, signaling.Request{
		Protocol: signaling.ProtocolRTPUDP,
		RTP:      &signaling.RTPRequest{Direction: c.direction, Address: address},
	})
	if err != nil {
		return signaling.Response{}, err
	}
	if session.RTP == nil {
		closeSession(signaler, session.ID)
		return signaling.Response{}, errors.New("session response has no RTP endpoint")
	}
	slog.Info("opened session", "id", session.ID, "rtp", session.RTP.Address)
	return session, nil
}

func (c *Client) runWebRTC(ctx context.Context, signaler *signaling.Client) error {
	send := c.direction == signaling.DirectionSend
	stdnet, err := ecnnet.New(
		ecnnet.SetRecvBufferSize(10_000_000),
		ecnnet.TrackECN(!send),
	)
	if err != nil {
		return err
	}
	setupCtx, cancelSetup := context.WithTimeout(ctx, webrtcConnectTimeout)
	defer cancelSetup()
	connectedCtx, cancelConnected := context.WithCancel(setupCtx)
	defer cancelConnected()

	runner := pipeline.NewRunner()
	defer func() {
		if closeErr := runner.Close(); closeErr != nil {
			slog.Error("failed to close pipeline", "error", closeErr)
		}
	}()
	var received frameCounter

	local := signaling.NewCandidates()
	options := []webrtc.Option{
		webrtc.SetNet(stdnet),
		webrtc.SetSRTPBufferLimit(10_000_000),
		webrtc.RegisterDefaultCodecs(),
		webrtc.RegisterFakeCodec(),
		webrtc.EnableNACK(),
		webrtc.EnableRTCPReports(),
		webrtc.SetICEServers(nil),
		webrtc.IncludeLoopbackCandidates(),
		webrtc.OnConnected(cancelConnected),
		webrtc.OnICECandidate(func(c *pionwebrtc.ICECandidateInit) {
			local.Push((*signaling.ICECandidate)(c))
		}),
	}
	if send {
		sendOptions, optErr := c.webrtcSendOptions()
		if optErr != nil {
			return optErr
		}
		options = append(options, sendOptions...)
	} else {
		options = append(options,
			webrtc.EnableCCFB(),
			webrtc.OnTrack(func(receiver *webrtc.RTPReceiver) {
				slog.Info("got track", "codec", receiver.Codec())
				g := pipeline.NewGraph()
				discard, trackErr := fakemedia.AddReceiver(g, receiver)
				if trackErr != nil {
					slog.Error("failed to receive track", "error", errors.Join(trackErr, g.Close()))
					return
				}
				// The run ends when the server ends the track.
				g.Terminal(receiver)
				received.add(discard)
				runner.Add(g)
			}),
		)
	}
	transport, err := webrtc.NewTransport(options...)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := transport.Close(); closeErr != nil {
			slog.Error("failed to close WebRTC transport", "error", closeErr)
		}
	}()
	if send {
		track, trackErr := transport.AddLocalTrackWithCodec(fakemedia.Codec.MimeType())
		if trackErr != nil {
			return trackErr
		}
		g := pipeline.NewGraph()
		runner.Add(g)
		source, sendErr := fakemedia.AddSender(g, c.senderConfig(), track)
		if sendErr != nil {
			return sendErr
		}
		transport.ControlBitrate(source)
	} else if err = transport.AddRemoteVideoTrack(); err != nil {
		return err
	}

	id, err := offerTrickle(setupCtx, signaler, transport, local)
	if err != nil {
		return err
	}
	slog.Info("opened session", "id", id, "protocol", signaling.ProtocolWebRTC)
	defer closeSession(signaler, id)

	<-connectedCtx.Done()
	if err = setupCtx.Err(); err != nil {
		return fmt.Errorf("peer connection not established within %v: %w", webrtcConnectTimeout, err)
	}

	if send {
		return runner.Run(ctx)
	}
	ctx, cancel := c.receiveContext(ctx)
	defer cancel()
	err = runner.Run(ctx)
	slog.Info("received", "frames", received.frames())
	return err
}

// webrtcSendOptions are the transport options of a sending client.
func (c *Client) webrtcSendOptions() ([]webrtc.Option, error) {
	options := []webrtc.Option{webrtc.EnableCCFBReceiver()}
	if c.traceRTP {
		options = append(options, webrtc.EnableRTPSendTraceLogging())
	}
	if c.pacing {
		options = append(options, webrtc.EnablePacing())
	}
	if c.bwe != "" {
		bweOptions, err := makeWebRTCBWE(c.bwe, BWEConfig{
			InitTargetRate: c.bitrate,
			MinTargetRate:  minTargetRate,
			MaxTargetRate:  c.maxTargetRate,
		})
		if err != nil {
			return nil, err
		}
		options = append(options, bweOptions...)
	}
	return options, nil
}

// frameCounter sums the frames of the tracks a client receives.
type frameCounter struct {
	lock     sync.Mutex
	discards []*pipeline.Discard[mrtp.EncodedFrame]
}

func (c *frameCounter) add(d *pipeline.Discard[mrtp.EncodedFrame]) {
	c.lock.Lock()
	defer c.lock.Unlock()
	c.discards = append(c.discards, d)
}

func (c *frameCounter) frames() uint64 {
	c.lock.Lock()
	defer c.lock.Unlock()
	var n uint64
	for _, d := range c.discards {
		n += d.Packets()
	}
	return n
}

// localIP returns the IP this host reaches the host of serverURL from. It
// sends nothing.
func localIP(serverURL string) (net.IP, error) {
	u, err := url.Parse(serverURL)
	if err != nil {
		return nil, err
	}
	conn, err := net.Dial("udp", net.JoinHostPort(u.Hostname(), "9"))
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP, nil
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

// runGraph runs g until it ends or ctx is done, and closes it.
func runGraph(ctx context.Context, g *pipeline.Graph) error {
	defer func() {
		if closeErr := g.Close(); closeErr != nil {
			slog.Error("failed to close pipeline", "error", closeErr)
		}
	}()
	if err := g.Run(ctx); err != nil && ctx.Err() == nil {
		return err
	}
	return nil
}
