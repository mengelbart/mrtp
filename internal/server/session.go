package server

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"

	"github.com/mengelbart/mrtp"
	"github.com/mengelbart/mrtp/internal/sessionmedia"
	"github.com/mengelbart/mrtp/pipeline"
)

// session is the media of one signaling session: the pipelines that send and
// receive over its transport. It runs them until they end or the session
// closes, and closes the transport once they end.
type session struct {
	id     string
	logger *slog.Logger
	media  *media
	// transport is nil until setup opens it. Closing it unblocks pending
	// reads and writes.
	transport io.Closer
	runner    *pipeline.Runner
	cancel    context.CancelFunc
	done      chan struct{}

	lock  sync.Mutex
	sinks []sessionmedia.FrameSink
}

func newSession(id string, m *media, logger *slog.Logger) *session {
	return &session{
		id:     id,
		logger: logger.With("id", id),
		media:  m,
		runner: pipeline.NewRunner(),
		done:   make(chan struct{}),
	}
}

// addSender sends what sender produces to sink. It returns what rate control
// steers, nil if the rate is fixed.
func (s *session) addSender(sender *sessionmedia.Sender, sink mrtp.Sink[mrtp.RTPPacket]) (mrtp.TargetBitrateSetter, error) {
	g := pipeline.NewGraph()
	s.runner.Add(g)
	return sender.Add(g, sink)
}

// addReceiver records the frames of codec that src receives in the session's
// next sink.
func (s *session) addReceiver(src mrtp.Source[mrtp.RTPPacket], codec mrtp.Codec) error {
	s.lock.Lock()
	defer s.lock.Unlock()
	sink, err := s.media.newSink(s.id, len(s.sinks), codec)
	if err != nil {
		return err
	}
	g := pipeline.NewGraph()
	g.Add(sink)
	if err = sessionmedia.AddReceiver(g, src, sink); err != nil {
		return errors.Join(err, g.Close())
	}
	s.sinks = append(s.sinks, sink)
	s.runner.Add(g)
	return nil
}

// abort releases a session whose setup failed with err.
func (s *session) abort(err error) error {
	if s.transport != nil {
		err = errors.Join(err, s.transport.Close())
	}
	return errors.Join(err, s.runner.Close())
}

// start runs the pipelines once ready is closed, or right away if ready is
// nil.
func (s *session) start(ready <-chan struct{}) {
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	go s.run(ctx, ready)
}

func (s *session) run(ctx context.Context, ready <-chan struct{}) {
	defer close(s.done)
	if ready != nil {
		select {
		case <-ready:
		case <-ctx.Done():
			return
		}
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
	// Closing the transport lets the client see the end of the stream.
	if err = s.transport.Close(); err != nil {
		s.logger.Error("failed to close transport", "error", err)
	}
}

// frames returns the number of frames received on all tracks.
func (s *session) frames() uint64 {
	s.lock.Lock()
	defer s.lock.Unlock()
	var n uint64
	for _, sink := range s.sinks {
		n += sink.Frames()
	}
	return n
}

// Close implements signaling.Session. It stops the pipelines and closes the
// transport.
func (s *session) Close() error {
	s.cancel()
	err := s.transport.Close()
	<-s.done
	err = errors.Join(err, s.runner.Close())
	s.logger.Info("closed session", "frames", s.frames())
	return err
}
