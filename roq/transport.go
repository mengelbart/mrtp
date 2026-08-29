package roq

import (
	"context"

	"github.com/mengelbart/roq"
	"github.com/quic-go/quic-go"
)

type Transport struct {
	session *roq.Session

	ctx context.Context
}

// New opens a RoQ session on quicConn. The session logs its RoQ events into
// the qlog trace of the QUIC connection, if the connection has one that
// declares the RoQ event schema.
func New(ctx context.Context, quicConn *quic.Conn) (*Transport, error) {
	s, err := roq.NewSessionWithAppHandledConn(NewQUICGoConnection(quicConn), true)
	if err != nil {
		return nil, err
	}

	return &Transport{
		session: s,
		ctx:     ctx,
	}, nil
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
	return newSender(t.ctx, flow, sendMode, logRTPpackets)
}

func (t *Transport) NewReceiveFlow(id uint64, logRTPpackets bool) (*Receiver, error) {
	flow, err := t.session.NewReceiveFlow(id)
	if err != nil {
		return nil, err
	}
	return newReciever(flow, logRTPpackets), nil
}

func (t *Transport) Close() error {
	return t.session.Close()
}
