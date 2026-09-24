package server

import (
	"context"
	"errors"
	"log/slog"
	"net"

	"github.com/mengelbart/mrtp"
	"github.com/mengelbart/mrtp/pipeline"
	"github.com/mengelbart/mrtp/udp"
)

// rtpUDPSession is one RTP over UDP stream whose packets are dropped.
type rtpUDPSession struct {
	id      string
	logger  *slog.Logger
	src     *udp.Source[mrtp.RTPPacket]
	graph   *pipeline.Graph
	discard *pipeline.Discard[mrtp.RTPPacket]
	cancel  context.CancelFunc
	done    chan struct{}
}

// newRTPUDPSession binds a UDP socket on host and starts dropping the RTP packets
// that arrive on it.
func newRTPUDPSession(id, host string, logger *slog.Logger) (*rtpUDPSession, error) {
	src, err := udp.Listen(net.JoinHostPort(host, "0"), false, mrtp.RTP{}, rtpBytes)
	if err != nil {
		return nil, err
	}
	discard := pipeline.NewDiscard[mrtp.RTPPacket]()

	g := pipeline.NewGraph()
	if err = g.Connect(src, discard); err != nil {
		_ = src.Close()
		return nil, err
	}
	g.Terminal(src)

	ctx, cancel := context.WithCancel(context.Background())
	sess := &rtpUDPSession{
		id:      id,
		logger:  logger.With("id", id),
		src:     src,
		graph:   g,
		discard: discard,
		cancel:  cancel,
		done:    make(chan struct{}),
	}
	go sess.run(ctx)
	return sess, nil
}

func (s *rtpUDPSession) run(ctx context.Context) {
	defer close(s.done)
	if err := s.graph.Run(ctx); err != nil && !errors.Is(err, net.ErrClosed) && ctx.Err() == nil {
		s.logger.Error("session pipeline failed", "error", err)
	}
}

// close stops the pipeline and releases the socket.
func (s *rtpUDPSession) close() error {
	s.cancel()
	// Closing the socket unblocks the pending Read.
	err := s.src.Close()
	<-s.done
	err = errors.Join(err, s.graph.Close())
	s.logger.Info("closed session", "packets", s.discard.Packets())
	return err
}

func rtpBytes(p *mrtp.RTPPacket) *[]byte { return &p.Data }
