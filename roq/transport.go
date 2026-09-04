package roq

import (
	"context"

	"github.com/mengelbart/mrtp"
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

// NewSendFlow opens the send flow with the given ID as a sink.
func NewSendFlow[T any](t *Transport, id uint64, sendMode SendMode, logRTPpackets bool, bytes func(*T) *[]byte) (*Sender[T], error) {
	flow, err := t.session.NewSendFlow(id)
	if err != nil {
		return nil, err
	}
	return newSender(t.ctx, flow, sendMode, logRTPpackets, bytes)
}

// NewReceiveFlow opens the receive flow with the given ID as a pushing source
// of packets of format f.
func NewReceiveFlow[T any](t *Transport, id uint64, logRTPpackets bool, f mrtp.Format, bytes func(*T) *[]byte) (*Receiver[T], error) {
	flow, err := t.session.NewReceiveFlow(id)
	if err != nil {
		return nil, err
	}
	r := &Receiver[T]{}
	r.init(flow, logRTPpackets, f, bytes)
	return r, nil
}

// NewReceivePuller opens the receive flow with the given ID as a puller of
// packets of format f.
func NewReceivePuller[T any](t *Transport, id uint64, logRTPpackets bool, f mrtp.Format, bytes func(*T) *[]byte) (*Puller[T], error) {
	flow, err := t.session.NewReceiveFlow(id)
	if err != nil {
		return nil, err
	}
	r := &Puller[T]{}
	r.init(flow, logRTPpackets, f, bytes)
	return r, nil
}

func (t *Transport) Close() error {
	return t.session.Close()
}
