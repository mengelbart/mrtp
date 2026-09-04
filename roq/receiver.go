package roq

import (
	"context"
	"errors"
	"math"

	"github.com/mengelbart/mrtp"
	"github.com/mengelbart/mrtp/internal/logging"
	"github.com/mengelbart/mrtp/pipeline"
	"github.com/mengelbart/roq"
)

// packetBufferSize is the size of the buffer one packet is read into. A flow
// reports a buffer smaller than the packet as io.ErrShortBuffer rather than
// truncating, so this is the largest a packet can be.
const packetBufferSize = math.MaxUint16

// recvFlow is what the two receiving elements share: the flow, the pool their
// packets come from, and the one read that fills a packet.
type recvFlow[T any] struct {
	flow   *roq.ReceiveFlow
	logger *logging.RTPLogger
	format mrtp.Format
	bytes  func(*T) *[]byte
	pool   *pipeline.Pool[T]
}

func (r *recvFlow[T]) init(flow *roq.ReceiveFlow, logRTPpackets bool, f mrtp.Format, bytes func(*T) *[]byte) {
	r.flow = flow
	r.format = f
	r.bytes = bytes
	r.pool = pipeline.NewPool(
		func() *T {
			var value T
			*bytes(&value) = make([]byte, packetBufferSize)
			return &value
		},
		func(value *T) {
			buffer := bytes(value)
			*buffer = (*buffer)[:cap(*buffer)]
		},
	)
	if logRTPpackets {
		r.logger = logging.NewRTPLogger("roq src", nil)
	}
}

// Format is what this flow's packets carry.
func (r *recvFlow[T]) Format() mrtp.Format {
	return r.format
}

// read takes the next packet of the flow as one owned packet.
func (r *recvFlow[T]) read() (mrtp.Packet[T], error) {
	packet := r.pool.Get()
	buffer := r.bytes(packet.Value())
	n, err := r.flow.Read(*buffer)
	if err != nil {
		packet.Release()
		return nil, err
	}
	*buffer = (*buffer)[:n]
	if r.logger != nil {
		r.logger.LogRTPPacketBuf(*buffer, nil)
	}
	return packet, nil
}

// Close implements mrtp.Element.
func (r *recvFlow[T]) Close() error {
	return r.flow.Close()
}

// Receiver receives packets on a RoQ receive flow and pushes them downstream.
type Receiver[T any] struct {
	recvFlow[T]
	down mrtp.Sink[T]
}

// Connect implements mrtp.Source.
func (r *Receiver[T]) Connect(down mrtp.Sink[T]) error {
	if r.down != nil {
		return errors.New("roq: receiver is already connected")
	}
	r.down = down
	return nil
}

// Run implements mrtp.Driver. It reads until the flow fails or is closed, or
// until ctx is cancelled.
func (r *Receiver[T]) Run(ctx context.Context) error {
	if r.down == nil {
		return errors.New("roq: receiver runs with its output wired")
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		packet, err := r.read()
		if err != nil {
			return err
		}
		if err := r.down.Write(packet); err != nil {
			return err
		}
	}
}

// Puller receives packets on a RoQ receive flow, one per Pull. It is the shape
// a flow nothing drives takes, such as the RTCP a pipeline reads only when it
// wants a report.
type Puller[T any] struct {
	recvFlow[T]
}

// Pull implements mrtp.Puller. It ignores ctx once the read has started, so
// Close only unblocks it because closing the flow fails the read.
func (r *Puller[T]) Pull(ctx context.Context) (mrtp.Packet[T], error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	return r.read()
}

var (
	_ mrtp.Source[mrtp.RTPPacket]  = (*Receiver[mrtp.RTPPacket])(nil)
	_ mrtp.Driver                  = (*Receiver[mrtp.RTPPacket])(nil)
	_ mrtp.Puller[mrtp.RTCPPacket] = (*Puller[mrtp.RTCPPacket])(nil)
)
