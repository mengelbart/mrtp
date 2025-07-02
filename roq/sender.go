package roq

import (
	"context"
	"errors"

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
}

func newSender(flow *roq.SendFlow, mode SendMode) (*Sender, error) {
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
	return sender, nil
}

func (s *Sender) Write(data []byte) (int, error) {
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
