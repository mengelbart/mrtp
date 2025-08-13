package roq

import (
	"context"
	"errors"

	"github.com/mengelbart/mrtp/logging"
	"github.com/mengelbart/roq"
)

type SendMode int

const (
	SendModeDatagram SendMode = iota
	SendModeStreamPerPacket
	SendModeSingleStream
)

type Sender struct {
	mode   SendMode
	flow   *roq.SendFlow
	stream *roq.RTPSendStream
	logger *logging.RTPLogger
}

func newSender(flow *roq.SendFlow, mode SendMode, logRTPpackets bool) (*Sender, error) {
	var err error
	var stream *roq.RTPSendStream
	if mode == SendModeSingleStream {
		stream, err = flow.NewSendStream(context.TODO())
		if err != nil {
			return nil, err
		}
	}
	sender := &Sender{
		mode:   mode,
		flow:   flow,
		stream: stream,
	}
	if logRTPpackets {
		sender.logger = logging.NewRTPLogger("roq sink", nil)
	}

	return sender, nil
}

func (s *Sender) Write(data []byte) (int, error) {
	// log rtp packet
	if s.logger != nil {
		s.logger.LogRTPPacketBuf(data, nil)
	}

	switch s.mode {
	case SendModeDatagram:
		return len(data), s.flow.WriteRTPBytes(data)
	case SendModeStreamPerPacket:
		stream, err := s.flow.NewSendStream(context.TODO())
		if err != nil {
			return 0, err
		}
		defer stream.Close()
		return stream.WriteRTPBytes(data)
	case SendModeSingleStream:
		return s.stream.WriteRTPBytes(data)
	}
	return 0, errors.New("invalid send mode")
}

func (s *Sender) Close() error {
	return s.flow.Close()
}
