package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"

	"github.com/mengelbart/mrtp"
	"github.com/mengelbart/mrtp/pipeline"
	"github.com/mengelbart/mrtp/signaling"
	"github.com/mengelbart/mrtp/webrtc"
	"github.com/mengelbart/mrtp/webrtc/ecnnet"
	pionwebrtc "github.com/pion/webrtc/v4"
)

const webrtcBufferSize = 10_000_000

// webrtcSession is one WebRTC peer connection. It sends fake video on every
// recvonly video m-line of the offer, and depacketizes and drops every track
// the client sends.
type webrtcSession struct {
	id        string
	logger    *slog.Logger
	transport *webrtc.Transport
	runner    *pipeline.Runner
	cancel    context.CancelFunc
	done      chan struct{}
	// candidates is nil unless the client trickles ICE.
	candidates *signaling.Candidates

	lock     sync.Mutex
	discards []*pipeline.Discard[mrtp.EncodedFrame]
}

// newWebRTCSession answers offer with a peer connection whose candidates are
// restricted to host. It generates CCFB, NACK and RTCP reports as far as the
// offer negotiates them. Unless the offer trickles ICE, it returns once the
// answer is complete.
func newWebRTCSession(ctx context.Context, id, host string, offer signaling.WebRTCOffer, logger *slog.Logger) (*webrtcSession, string, error) {
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
		id:     id,
		logger: logger.With("id", id),
		runner: pipeline.NewRunner(),
		cancel: cancel,
		done:   make(chan struct{}),
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
	for range sess.transport.RequestedVideoTracks() {
		if err = sess.addSender(); err != nil {
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

// addSender sends fake video on a new local track.
func (s *webrtcSession) addSender() error {
	track, err := s.transport.AddLocalTrackWithCodec(mediaCodec.MimeType())
	if err != nil {
		return err
	}
	g := pipeline.NewGraph()
	s.runner.Add(g)
	source, err := addSender(g, track)
	if err != nil {
		return err
	}
	s.transport.ControlBitrate(source)
	return nil
}

func (s *webrtcSession) onTrack(receiver *webrtc.RTPReceiver) {
	s.logger.Info("got track", "codec", receiver.Codec())
	g := pipeline.NewGraph()
	discard, err := addReceiver(g, receiver)
	if err != nil {
		s.logger.Error("failed to receive track", "error", errors.Join(err, g.Close()))
		return
	}
	s.lock.Lock()
	s.discards = append(s.discards, discard)
	s.lock.Unlock()
	s.runner.Add(g)
}

func (s *webrtcSession) run(ctx context.Context) {
	defer close(s.done)
	if err := s.runner.Run(ctx); err != nil && ctx.Err() == nil {
		s.logger.Error("session pipeline failed", "error", err)
	}
}

// frames returns the number of frames received on all tracks.
func (s *webrtcSession) frames() uint64 {
	s.lock.Lock()
	defer s.lock.Unlock()
	var n uint64
	for _, d := range s.discards {
		n += d.Packets()
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
