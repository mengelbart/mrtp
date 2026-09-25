package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"

	"github.com/mengelbart/mrtp/internal/sessionmedia"
	"github.com/mengelbart/mrtp/pipeline"
	"github.com/mengelbart/mrtp/signaling"
	"github.com/mengelbart/mrtp/webrtc"
	"github.com/mengelbart/mrtp/webrtc/ecnnet"
	pionwebrtc "github.com/pion/webrtc/v4"
)

const webrtcBufferSize = 10_000_000

// webrtcSession is one WebRTC peer connection. It sends fake video or a
// source file on every recvonly video m-line of the offer, and receives every
// track the client sends. It closes the peer connection once what it sends
// ends.
type webrtcSession struct {
	id        string
	logger    *slog.Logger
	transport *webrtc.Transport
	media     *media
	// runner starts once the peer connection is up, so that nothing is sent
	// before the client can receive it.
	runner        *pipeline.Runner
	connected     chan struct{}
	connectedOnce sync.Once
	cancel        context.CancelFunc
	done          chan struct{}
	// candidates is nil unless the client trickles ICE.
	candidates *signaling.Candidates

	lock  sync.Mutex
	sinks []frameSink
}

// newWebRTCSession answers offer with a peer connection whose candidates are
// restricted to host. It generates CCFB, NACK and RTCP reports as far as the
// offer negotiates them. Unless the offer trickles ICE, it returns once the
// answer is complete.
func newWebRTCSession(ctx context.Context, id, host string, offer signaling.WebRTCOffer, source string, m *media, logger *slog.Logger) (*webrtcSession, string, error) {
	ip := net.ParseIP(host)
	if ip == nil {
		return nil, "", fmt.Errorf("media host %q is not an IP address", host)
	}
	stdnet, err := ecnnet.New(
		ecnnet.SetRecvBufferSize(webrtcBufferSize),
		ecnnet.TrackECN(true),
	)
	if err != nil {
		return nil, "", err
	}

	runCtx, cancel := context.WithCancel(context.Background())
	sess := &webrtcSession{
		id:        id,
		logger:    logger.With("id", id),
		media:     m,
		runner:    pipeline.NewRunner(),
		connected: make(chan struct{}),
		cancel:    cancel,
		done:      make(chan struct{}),
	}
	options := []webrtc.Option{
		webrtc.SetNet(stdnet),
		webrtc.SetSRTPBufferLimit(webrtcBufferSize),
		webrtc.RegisterDefaultCodecs(),
		webrtc.RegisterFakeCodec(),
		webrtc.EnableCCFB(),
		webrtc.EnableNACK(),
		webrtc.EnableRTCPReports(),
		webrtc.SetICEServers(nil),
		webrtc.IncludeLoopbackCandidates(),
		webrtc.ListenIP(ip),
		webrtc.OnTrack(sess.onTrack),
		webrtc.OnConnected(func() { sess.connectedOnce.Do(func() { close(sess.connected) }) }),
	}
	if offer.Trickle {
		sess.candidates = signaling.NewCandidates()
		options = append(options, webrtc.OnICECandidate(func(c *pionwebrtc.ICECandidateInit) {
			sess.candidates.Push((*signaling.ICECandidate)(c))
		}))
	}
	sess.transport, err = webrtc.NewTransport(options...)
	if err != nil {
		cancel()
		return nil, "", err
	}
	if err = sess.transport.SetOffer(offer.SDP); err != nil {
		cancel()
		return nil, "", errors.Join(fmt.Errorf("%w: invalid webrtc offer: %w", signaling.ErrBadRequest, err), sess.transport.Close())
	}
	requested := sess.transport.RequestedVideoTracks()
	if source != "" && requested == 0 {
		cancel()
		return nil, "", errors.Join(fmt.Errorf("%w: source requires a recvonly video m-line", signaling.ErrBadRequest), sess.transport.Close())
	}
	for range requested {
		if err = sess.addSender(source); err != nil {
			cancel()
			return nil, "", errors.Join(err, sess.transport.Close(), sess.runner.Close())
		}
	}
	answer, err := sess.transport.CreateAnswer(ctx)
	if err != nil {
		cancel()
		return nil, "", errors.Join(err, sess.transport.Close(), sess.runner.Close())
	}
	go sess.run(runCtx)
	return sess, answer, nil
}

// addSender sends source on a new local track.
func (s *webrtcSession) addSender(source string) error {
	sender, err := s.media.newSender(source)
	if err != nil {
		return err
	}
	track, err := s.transport.AddLocalTrackWithCodec(sender.codec.MimeType())
	if err != nil {
		return errors.Join(err, sender.close())
	}
	g := pipeline.NewGraph()
	s.runner.Add(g)
	rate, err := sender.add(g, track)
	if err != nil {
		return err
	}
	if rate != nil {
		s.transport.ControlBitrate(rate)
	}
	return nil
}

func (s *webrtcSession) onTrack(receiver *webrtc.RTPReceiver) {
	s.logger.Info("got track", "codec", receiver.Codec())
	s.lock.Lock()
	defer s.lock.Unlock()
	sink, err := s.media.newSink(s.id, len(s.sinks), receiver.Codec())
	if err != nil {
		s.logger.Error("failed to receive track", "error", errors.Join(err, receiver.Close()))
		return
	}
	g := pipeline.NewGraph()
	if err = sessionmedia.AddReceiver(g, receiver, sink); err != nil {
		s.logger.Error("failed to receive track", "error", errors.Join(err, g.Close()))
		return
	}
	s.sinks = append(s.sinks, sink)
	s.runner.Add(g)
}

func (s *webrtcSession) run(ctx context.Context) {
	defer close(s.done)
	select {
	case <-s.connected:
	case <-ctx.Done():
		return
	}
	err := s.runner.Run(ctx)
	if ctx.Err() != nil {
		return
	}
	if err != nil {
		s.logger.Error("session pipeline failed", "error", err)
	} else {
		s.logger.Info("stream ended")
	}
	// Closing the peer connection ends the client's tracks.
	if err = s.transport.Close(); err != nil {
		s.logger.Error("failed to close peer connection", "error", err)
	}
}

// frames returns the number of frames received on all tracks.
func (s *webrtcSession) frames() uint64 {
	s.lock.Lock()
	defer s.lock.Unlock()
	var n uint64
	for _, sink := range s.sinks {
		n += sink.Frames()
	}
	return n
}

// AddICECandidate implements signaling.TrickleSession.
func (s *webrtcSession) AddICECandidate(candidate signaling.ICECandidate) error {
	return s.transport.AddICECandidate(pionwebrtc.ICECandidateInit(candidate))
}

// LocalCandidates implements signaling.TrickleSession.
func (s *webrtcSession) LocalCandidates() *signaling.Candidates {
	return s.candidates
}

// Close implements signaling.Session. It stops the pipelines and the peer
// connection.
func (s *webrtcSession) Close() error {
	s.cancel()
	// Closing the peer connection unblocks pending track reads.
	err := s.transport.Close()
	<-s.done
	err = errors.Join(err, s.runner.Close())
	s.logger.Info("closed session", "frames", s.frames())
	return err
}
