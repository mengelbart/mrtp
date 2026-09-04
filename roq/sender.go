package roq

import (
	"context"
	"errors"

	"github.com/mengelbart/mrtp"
	"github.com/mengelbart/mrtp/internal/logging"
	"github.com/mengelbart/roq"
)

type SendMode int

const (
	SendModeDatagram SendMode = iota
	SendModeStreamPerPacket
	SendModeSingleStream
)

// Sender sends packets on a RoQ send flow. One packet is one datagram, one
// stream, or one length prefixed frame on a shared stream, depending on the
// mapping.
type Sender[T any] struct {
	mode   SendMode
	flow   *roq.SendFlow
	stream *roq.RTPSendStream
	logger *logging.RTPLogger
	bytes  func(*T) *[]byte
	ctx    context.Context
}

func newSender[T any](ctx context.Context, flow *roq.SendFlow, mode SendMode, logRTPpackets bool, bytes func(*T) *[]byte) (*Sender[T], error) {
	var err error
	var stream *roq.RTPSendStream
	if mode == SendModeSingleStream {
		stream, err = flow.NewSendStream(ctx, 1, true)
		if err != nil {
			return nil, err
		}
	}
	sender := &Sender[T]{
		mode:   mode,
		flow:   flow,
		stream: stream,
		bytes:  bytes,
		ctx:    ctx,
	}
	if logRTPpackets {
		sender.logger = logging.NewRTPLogger("roq sink", nil)
	}

	return sender, nil
}

// Negotiate implements mrtp.Sink. A flow takes any format, because it sends
// bytes and configures nothing.
func (s *Sender[T]) Negotiate(mrtp.Format) error {
	return nil
}

// Write implements mrtp.Sink, sending one packet.
func (s *Sender[T]) Write(p mrtp.Packet[T]) error {
	defer p.Release()
	data := *s.bytes(p.Value())

	if s.logger != nil {
		s.logger.LogRTPPacketBuf(data, nil)
	}

	switch s.mode {
	case SendModeDatagram:
		return s.flow.WriteRTPBytes(data)
	case SendModeStreamPerPacket:
		stream, err := s.flow.NewSendStream(s.ctx, 1, false)
		if err != nil {
			return err
		}
		defer func() { _ = stream.Close() }()
		_, err = stream.WriteRTPBytes(data)
		return err
	case SendModeSingleStream:
		_, err := s.stream.WriteRTPBytes(data)
		return err
	}
	return errors.New("roq: invalid send mode")
}

// EndOfStream implements mrtp.Sink. A flow outlives the stream that ended, and
// is closed rather than ended.
func (s *Sender[T]) EndOfStream() error {
	return nil
}

// Close implements mrtp.Element.
func (s *Sender[T]) Close() error {
	return s.flow.Close()
}

var _ mrtp.Sink[mrtp.RTPPacket] = (*Sender[mrtp.RTPPacket])(nil)
