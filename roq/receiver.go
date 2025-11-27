package roq

import (
	"time"

	"github.com/mengelbart/mrtp/internal/logging"
	"github.com/mengelbart/roq"
)

// Receiver is a wrapper for roq.ReceiveFlow that supports logging RTP packets.
type Receiver struct {
	flow   *roq.ReceiveFlow
	logger *logging.RTPLogger
}

func newReciever(flow *roq.ReceiveFlow, logRTPpackets bool) *Receiver {
	receiver := &Receiver{
		flow: flow,
	}
	if logRTPpackets {
		receiver.logger = logging.NewRTPLogger("roq src", nil)
	}

	return receiver
}

func (r *Receiver) Read(buf []byte) (int, error) {
	n, err := r.flow.Read(buf)
	if err != nil {
		return n, err
	}

	// log rtp packet
	if r.logger != nil {
		r.logger.LogRTPPacketBuf(buf[:n], nil)
	}

	return n, nil
}

func (r *Receiver) SetReadDeadline(t time.Time) error {
	return r.flow.SetReadDeadline(t)
}

func (r *Receiver) ID() uint64 {
	return r.flow.ID()
}

func (r *Receiver) Close() error {
	return r.flow.Close()
}
