package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"

	"github.com/mengelbart/mrtp"
	"github.com/mengelbart/mrtp/internal/sessionmedia"
	"github.com/mengelbart/mrtp/pipeline"
	"github.com/mengelbart/mrtp/signaling"
	"github.com/mengelbart/mrtp/udp"
)

// rtpUDPSession is one RTP over UDP stream. The server either receives from
// the client, or sends fake video or a source file to it.
type rtpUDPSession struct {
	id     string
	logger *slog.Logger
	// addr is the server's end of the stream.
	addr   *net.UDPAddr
	socket io.Closer
	graph  *pipeline.Graph

	sending bool
	codec   mrtp.Codec

	// sink is nil if the server sends.
	sink   sessionmedia.FrameSink
	cancel context.CancelFunc
	done   chan struct{}
}

// newRTPUDPSession binds a UDP socket on host and starts the stream request
// asks for, sending source if the server sends.
func newRTPUDPSession(id, host string, request signaling.RTPRequest, source string, m *media, logger *slog.Logger) (*rtpUDPSession, error) {
	sess := &rtpUDPSession{
		id:     id,
		logger: logger.With("id", id),
		done:   make(chan struct{}),
	}
	var err error
	switch request.Direction {
	case signaling.DirectionSend:
		if source != "" {
			return nil, fmt.Errorf("%w: source requires direction %q", signaling.ErrBadRequest, signaling.DirectionRecv)
		}
		err = sess.receive(host, request, m)
	case signaling.DirectionRecv:
		err = sess.send(host, request.Address, source, m)
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

// receive takes the stream request describes on a socket bound on host.
func (s *rtpUDPSession) receive(host string, request signaling.RTPRequest, m *media) error {
	codec, err := mrtp.NewCodec(request.Codec)
	if err != nil {
		return fmt.Errorf("%w: invalid rtp codec %q", signaling.ErrBadRequest, request.Codec)
	}
	format, err := mrtp.NewRTPFormat(codec, int(request.PayloadType))
	if err != nil {
		return fmt.Errorf("%w: invalid rtp format: %w", signaling.ErrBadRequest, err)
	}
	if s.sink, err = m.newSink(s.id, 0, format.Codec); err != nil {
		return err
	}
	s.graph = pipeline.NewGraph()
	s.graph.Add(s.sink)
	src, err := udp.Listen(net.JoinHostPort(host, "0"), false, format, rtpBytes)
	if err != nil {
		return errors.Join(err, s.graph.Close())
	}
	if err = sessionmedia.AddReceiver(s.graph, src, s.sink); err != nil {
		return errors.Join(err, s.graph.Close())
	}
	s.graph.Terminal(src)
	s.addr = src.LocalAddr()
	s.socket = src
	return nil
}

// send sends source from a socket bound on host to address.
func (s *rtpUDPSession) send(host, address, source string, m *media) error {
	if address == "" {
		return fmt.Errorf("%w: missing rtp address", signaling.ErrBadRequest)
	}
	sender, err := m.newSender(source)
	if err != nil {
		return err
	}
	sink, err := udp.DialFrom(address, net.JoinHostPort(host, "0"), false, rtpBytes)
	if err != nil {
		return errors.Join(fmt.Errorf("%w: invalid rtp address: %w", signaling.ErrBadRequest, err), sender.Close())
	}
	s.graph = pipeline.NewGraph()
	if _, err = sender.Add(s.graph, sink); err != nil {
		return errors.Join(err, s.graph.Close())
	}
	s.sending = true
	s.codec = sender.Codec
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
	if s.sink != nil {
		s.logger.Info("closed session", "frames", s.sink.Frames())
	} else {
		s.logger.Info("closed session")
	}
	return err
}

func rtpBytes(p *mrtp.RTPPacket) *[]byte { return &p.Data }
