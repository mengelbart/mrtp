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
)

const webrtcBufferSize = 10_000_000

// webrtcSession is one WebRTC peer connection whose incoming RTP is dropped.
type webrtcSession struct {
	id        string
	logger    *slog.Logger
	transport *webrtc.Transport
	runner    *pipeline.Runner
	cancel    context.CancelFunc
	done      chan struct{}

	lock     sync.Mutex
	discards []*pipeline.Discard[mrtp.RTPPacket]
}

// errInvalidOffer marks errors caused by the client's request.
var errInvalidOffer = errors.New("invalid webrtc offer")

// newWebRTCSession answers offer with a peer connection whose candidates are
// restricted to host. It generates CCFB, NACK and RTCP reports as far as the
// offer negotiates them, and returns once the answer is complete.
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
	sess.transport, err = webrtc.NewTransport(nil, false,
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
	)
	if err != nil {
		cancel()
		return nil, "", err
	}
	answer, err := sess.transport.Answer(ctx, offer.SDP)
	if err != nil {
		cancel()
		return nil, "", errors.Join(fmt.Errorf("%w: %w", errInvalidOffer, err), sess.transport.Close())
	}
	go sess.run(runCtx)
	return sess, answer, nil
}

func (s *webrtcSession) onTrack(receiver *webrtc.RTPReceiver) {
	s.logger.Info("got track", "codec", receiver.Codec())
	discard := pipeline.NewDiscard[mrtp.RTPPacket]()
	g := pipeline.NewGraph()
	if err := g.Connect(receiver, discard); err != nil {
		s.logger.Error("failed to connect track", "error", err)
		_ = receiver.Close()
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

// packets returns the number of RTP packets received on all tracks.
func (s *webrtcSession) packets() uint64 {
	s.lock.Lock()
	defer s.lock.Unlock()
	var n uint64
	for _, d := range s.discards {
		n += d.Packets()
	}
	return n
}

// close stops the pipelines and the peer connection.
func (s *webrtcSession) close() error {
	s.cancel()
	// Closing the peer connection unblocks pending track reads.
	err := s.transport.Close()
	<-s.done
	err = errors.Join(err, s.runner.Close())
	s.logger.Info("closed session", "packets", s.packets())
	return err
}
