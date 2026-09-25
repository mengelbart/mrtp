package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"

	"github.com/mengelbart/mrtp"
	"github.com/mengelbart/mrtp/pipeline"
	"github.com/mengelbart/mrtp/signaling"
	"github.com/mengelbart/mrtp/udp"
)

// rtpUDPSession is one RTP over UDP stream of fake video. The server either
// depacketizes and drops what the client sends, or sends to the client.
type rtpUDPSession struct {
	id     string
	logger *slog.Logger
	// addr is the server's end of the stream.
	addr   *net.UDPAddr
	socket io.Closer
	graph  *pipeline.Graph
	// discard is nil if the server sends.
	discard *pipeline.Discard[mrtp.EncodedFrame]
	cancel  context.CancelFunc
	done    chan struct{}
}

// newRTPUDPSession binds a UDP socket on host and starts the stream request
// asks for.
func newRTPUDPSession(id, host string, request signaling.RTPRequest, logger *slog.Logger) (*rtpUDPSession, error) {
	sess := &rtpUDPSession{
		id:     id,
		logger: logger.With("id", id),
		done:   make(chan struct{}),
	}
	var err error
	switch request.Direction {
	case signaling.DirectionSend:
		err = sess.receive(host)
	case signaling.DirectionRecv:
		err = sess.send(host, request.Address)
	default:
		return nil, fmt.Errorf("%w: unknown direction %q", signaling.ErrBadRequest, request.Direction)
	}
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	sess.cancel = cancel
	go sess.run(ctx)
	return sess, nil
}

// receive drops the frames that arrive on a socket bound on host.
func (s *rtpUDPSession) receive(host string) error {
	format, err := mrtp.NewRTPFormat(mediaCodec, mrtp.DefaultPayloadType)
	if err != nil {
		return err
	}
	src, err := udp.Listen(net.JoinHostPort(host, "0"), false, format, rtpBytes)
	if err != nil {
		return err
	}
	s.graph = pipeline.NewGraph()
	if s.discard, err = addReceiver(s.graph, src); err != nil {
		return errors.Join(err, s.graph.Close())
	}
	s.graph.Terminal(src)
	s.addr = src.LocalAddr()
	s.socket = src
	return nil
}

// send sends from a socket bound on host to address.
func (s *rtpUDPSession) send(host, address string) error {
	if address == "" {
		return fmt.Errorf("%w: missing rtp address", signaling.ErrBadRequest)
	}
	sink, err := udp.DialFrom(address, net.JoinHostPort(host, "0"), false, rtpBytes)
	if err != nil {
		return fmt.Errorf("%w: invalid rtp address: %w", signaling.ErrBadRequest, err)
	}
	s.graph = pipeline.NewGraph()
	if _, err = addSender(s.graph, sink); err != nil {
		return errors.Join(err, s.graph.Close())
	}
	s.addr = sink.LocalAddr()
	s.socket = sink
	return nil
}

func (s *rtpUDPSession) run(ctx context.Context) {
	defer close(s.done)
	if err := s.graph.Run(ctx); err != nil && !errors.Is(err, net.ErrClosed) && ctx.Err() == nil {
		s.logger.Error("session pipeline failed", "error", err)
	}
}

// Close implements signaling.Session. It stops the pipeline and releases the
// socket.
func (s *rtpUDPSession) Close() error {
	s.cancel()
	// Closing the socket unblocks a pending Read or Write.
	err := s.socket.Close()
	<-s.done
	err = errors.Join(err, s.graph.Close())
	if s.discard != nil {
		s.logger.Info("closed session", "frames", s.discard.Packets())
	} else {
		s.logger.Info("closed session")
	}
	return err
}

func rtpBytes(p *mrtp.RTPPacket) *[]byte { return &p.Data }
