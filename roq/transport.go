package roq

import (
	"os"

	"github.com/mengelbart/qlog"
	"github.com/mengelbart/roq"
	"github.com/quic-go/quic-go"
)

type Option func(*Transport) error

func EnableRoqLogs(filepath string) Option {
	return func(d *Transport) error {
		d.logFile = filepath
		return nil
	}
}

type Transport struct {
	session *roq.Session
	logFile string
}

func New(quicConn *quic.Conn, opts ...Option) (*Transport, error) {
	t := &Transport{
		session: nil,
	}

	for _, opt := range opts {
		if err := opt(t); err != nil {
			return nil, err
		}
	}

	conn := roq.NewQUICGoConnection(quicConn)

	ql := (*qlog.Logger)(nil)

	if t.logFile != "" {
		f, err := os.OpenFile(t.logFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
		if err != nil {
			return nil, err
		}

		ql = qlog.NewQLOGHandler(f, "roq logs", "", "")
	}

	s, err := roq.NewSessionWithAppHandeledConn(conn, true, ql)
	if err != nil {
		return nil, err
	}

	t.session = s
	return t, nil
}

func (t *Transport) HandleDatagram(datagram []byte) {
	t.session.HandleDatagram(datagram)
}

func (t *Transport) HandleUniStreamWithFlowID(flowID uint64, rs roq.ReceiveStream) {
	t.session.HandleUniStreamWithFlowID(flowID, rs)
}

func (t *Transport) NewSendFlow(id uint64, sendMode SendMode, logRTPpackets bool) (*Sender, error) {
	flow, err := t.session.NewSendFlow(id)
	if err != nil {
		return nil, err
	}
	return newSender(flow, sendMode, logRTPpackets)
}

func (t *Transport) NewReceiveFlow(id uint64, logRTPpackets bool) (*Receiver, error) {
	flow, err := t.session.NewReceiveFlow(id)
	if err != nil {
		return nil, err
	}
	return newReciever(flow, logRTPpackets), nil
}
