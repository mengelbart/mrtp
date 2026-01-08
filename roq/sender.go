package roq

import (
	"context"
	"errors"
	"time"

	"github.com/mengelbart/mrtp/internal/logging"
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
		stream, err = flow.NewSendStream(context.TODO(), 1, true)
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

func cancleClose(stream *roq.RTPSendStream) {
	stream.Close()

	go func() {
		time.Sleep(500 * time.Millisecond) // 500 * time.Millisecond
		stream.CancelStream(0)
	}()
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
		stream, err := s.flow.NewSendStream(context.TODO(), 1, true)
		if err != nil {
			return 0, err
		}
		defer cancleClose(stream)
		return stream.WriteRTPBytes(data)
	case SendModeSingleStream:
		return s.stream.WriteRTPBytes(data)
	}
	return 0, errors.New("invalid send mode")
}

func (s *Sender) Close() error {
	return s.flow.Close()
}
