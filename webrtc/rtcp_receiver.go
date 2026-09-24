package webrtc

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/mengelbart/mrtp"
	"github.com/mengelbart/mrtp/pipeline"
	"github.com/pion/interceptor"
	"github.com/pion/interceptor/pkg/rtpfb"
)

type pionRTCPReceiver interface {
	Read([]byte) (int, interceptor.Attributes, error)
}

// rtcpQueueDepth is how many pulled RTCP packets are buffered for a consumer
// before the oldest is dropped.
const rtcpQueueDepth = 8

// RTCPReceiver pumps RTCP off a pion interceptor chain into a buffer a
// pipeline can pull from. Its track starts the pump when it is created and it
// runs until the track stops, because reading is what lets the chain deliver
// the feedback the congestion controller runs on. Packets are buffered only
// once the receiver is requested, and a full queue drops its oldest packet
// rather than block.
type RTCPReceiver struct {
	receiver pionRTCPReceiver
	onCCFB   func(rtpfb.Report) error
	wanted   atomic.Bool

	pool  *pipeline.Pool[mrtp.RTCPPacket]
	queue chan mrtp.Packet[mrtp.RTCPPacket]
	drops atomic.Int64

	closeOnce sync.Once
	done      chan struct{}
	stopped   chan struct{}
}

func newRTCPReceiver(receiver pionRTCPReceiver, onCCFB func(rtpfb.Report) error) *RTCPReceiver {
	r := &RTCPReceiver{
		receiver: receiver,
		onCCFB:   onCCFB,
		pool: pipeline.NewPool(
			func() *mrtp.RTCPPacket {
				return &mrtp.RTCPPacket{Data: make([]byte, packetBufferSize)}
			},
			func(p *mrtp.RTCPPacket) {
				p.Data = p.Data[:cap(p.Data)]
			},
		),
		queue:   make(chan mrtp.Packet[mrtp.RTCPPacket], rtcpQueueDepth),
		done:    make(chan struct{}),
		stopped: make(chan struct{}),
	}
	go r.run()
	return r
}

func (r *RTCPReceiver) run() {
	defer func() {
		r.drain()
		close(r.stopped)
	}()
	for {
		packet := r.pool.Get()
		n, attr, err := r.receiver.Read(packet.Value().Data)
		if err != nil {
			packet.Release()
			return
		}
		packet.Value().Data = packet.Value().Data[:n]

		if report, ok := attr.Get(rtpfb.CCFBAttributesKey).(rtpfb.Report); ok && r.onCCFB != nil {
			if err := r.onCCFB(report); err != nil {
				slog.Error("failed to handle congestion control feedback", "error", err)
			}
		}
		if !r.wanted.Load() || r.isClosed() {
			packet.Release()
			continue
		}
		r.enqueue(packet)
		if r.isClosed() {
			r.drain()
		}
	}
}

// want marks the receiver as requested, so that the pump buffers packets.
func (r *RTCPReceiver) want() *RTCPReceiver {
	r.wanted.Store(true)
	return r
}

func (r *RTCPReceiver) isClosed() bool {
	select {
	case <-r.done:
		return true
	default:
		return false
	}
}

// enqueue buffers packet, making room by dropping the oldest packet if needed.
func (r *RTCPReceiver) enqueue(packet mrtp.Packet[mrtp.RTCPPacket]) {
	select {
	case r.queue <- packet:
		return
	default:
	}
	select {
	case old := <-r.queue:
		old.Release()
		r.drops.Add(1)
	default:
	}
	select {
	case r.queue <- packet:
	default:
		packet.Release()
		r.drops.Add(1)
	}
}

// drain releases what the queue still holds.
func (r *RTCPReceiver) drain() {
	for {
		select {
		case p := <-r.queue:
			p.Release()
		default:
			return
		}
	}
}

// Format implements mrtp.Puller.
func (r *RTCPReceiver) Format() mrtp.Format {
	return mrtp.RTCP{}
}

// Pull implements mrtp.Puller. It reports io.EOF once the receiver has stopped
// and its queued packets are drained.
func (r *RTCPReceiver) Pull(ctx context.Context) (mrtp.Packet[mrtp.RTCPPacket], error) {
	select {
	case p := <-r.queue:
		return p, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-r.done:
	case <-r.stopped:
	}
	select {
	case p := <-r.queue:
		return p, nil
	default:
		return nil, io.EOF
	}
}

// Close implements mrtp.Element. It stops buffering and releases what was
// buffered. The pump keeps running for the congestion controller until the
// track stops.
func (r *RTCPReceiver) Close() error {
	r.closeOnce.Do(func() {
		close(r.done)
		if drops := r.drops.Load(); drops > 0 {
			slog.Warn("dropped RTCP packets no consumer pulled in time", "count", drops)
		}
	})
	r.drain()
	return nil
}

var _ mrtp.Puller[mrtp.RTCPPacket] = (*RTCPReceiver)(nil)
