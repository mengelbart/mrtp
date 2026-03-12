package gopipe

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/mengelbart/mrtp/gopipe/codec"
	"github.com/mengelbart/mrtp/internal/logging"
	"github.com/pion/interceptor/pkg/jitterbuffer"
	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
)

// rtpDepacketizer is the actual depacketizer implementation
type rtpDepacketizer struct {
	jitterBuffer *jitterbuffer.JitterBuffer
	frameBuffer  []byte
	onFrame      func([]byte, int64) // callback for complete frames

	ctx     context.Context
	cancel  context.CancelFunc
	trigger chan struct{}

	missedPacketTime *time.Time
	fastSkip         bool   // skip missing packets immediately after first timeout until buffer drains
	playoutTs        uint32 // playout timestamp of the current frame being assembled
	codec            codec.CodecType

	maxTimeout     time.Duration
	currentTimeout atomic.Int64

	vp8Depacketizer  codecs.VP8Packet
	vp9Depacketizer  codecs.VP9Packet
	h264Depacketizer codecs.H264Packet

	unwrapper *logging.Unwrapper // for logging the rtp packets
}

func newRTPDepacketizer(maxTimeout time.Duration, c codec.CodecType, onFrame func(encFrame []byte, pts int64)) (*rtpDepacketizer, error) {
	if c != codec.VP8 && c != codec.VP9 && c != codec.H264 {
		return nil, fmt.Errorf("unsupported codec for depacketizer: %s", c.String())
	}

	ctx, cancel := context.WithCancel(context.Background())
	d := &rtpDepacketizer{
		jitterBuffer: jitterbuffer.New(),
		frameBuffer:  make([]byte, 0, 2000),
		onFrame:      onFrame,
		ctx:          ctx,
		cancel:       cancel,
		trigger:      make(chan struct{}, 1),
		maxTimeout:   maxTimeout,
		unwrapper:    &logging.Unwrapper{},
		codec:        c,
	}
	d.currentTimeout.Store(int64(maxTimeout))
	return d, nil
}

func (d *rtpDepacketizer) UpdateRTT(rtt time.Duration) {
	if rtt <= 0 {
		return
	}

	timeout := time.Duration(float64(rtt) * 1.5)
	timeout = min(timeout, d.maxTimeout)

	d.currentTimeout.Store(int64(timeout))
}

// Write just pushes to jitter buffer
func (d *rtpDepacketizer) Write(rtpBuf []byte) error {
	// copy rtp data to avoid memory reuse
	rtpBufCopy := make([]byte, len(rtpBuf))
	copy(rtpBufCopy, rtpBuf)

	pkt := new(rtp.Packet)
	if err := pkt.Unmarshal(rtpBufCopy); err != nil {
		return err
	}

	d.jitterBuffer.Push(pkt)

	// Signal that new packet is available
	select {
	case d.trigger <- struct{}{}:
	default:
	}

	return nil
}

// Run processes packets and assembles frames
func (d *rtpDepacketizer) Run() {
	for {
		select {
		case <-d.ctx.Done():
			return
		case <-d.trigger:
			d.processPackets()
		}
	}
}

func (d *rtpDepacketizer) processPackets() {
	droppingFrame := false
	for {
		_, err := d.jitterBuffer.Peek(true)
		if err == jitterbuffer.ErrBufferUnderrun {
			// buffer is empty - reset skip state
			if d.fastSkip {
				slog.Info("packetizer fast-skip done, buffer dra./plined")
			}
			d.fastSkip = false
			d.missedPacketTime = nil
			return
		}

		pkt, err := d.jitterBuffer.Pop()
		if err == jitterbuffer.ErrPopWhileBuffering {
			// still buffering - wait for more packets
			return
		}
		if err == jitterbuffer.ErrNotFound {
			// missing packet
			if d.fastSkip {
				// already timed out once - skip immediately to avoid cascading delay
				playoutHead := d.jitterBuffer.PlayoutHead()

				slog.Info("packitzier fast-skipping lost packet", "seqnr", playoutHead)

				d.jitterBuffer.SetPlayoutHead(playoutHead + 1)
				d.frameBuffer = d.frameBuffer[:0]
				droppingFrame = true
				continue
			}
			if d.missedPacketTime == nil {
				slog.Info("packitzier misses packet; start timeout", "seqnr", d.jitterBuffer.PlayoutHead())

				// start new timeout
				now := time.Now()
				d.missedPacketTime = &now
				return
			} else if time.Since(*d.missedPacketTime) > time.Duration(d.currentTimeout.Load()) {
				// timeout expired, drop current frame and enter fast-skip mode
				playoutHead := d.jitterBuffer.PlayoutHead()

				slog.Info("packitzier dropping frame, rtp packet lost", "seqnr", playoutHead)

				d.jitterBuffer.SetPlayoutHead(playoutHead + 1)
				d.frameBuffer = d.frameBuffer[:0]
				droppingFrame = true
				d.fastSkip = true
				continue
			}

			// still waiting for missing packet
			return
		}
		if err != nil {
			slog.Error("Depackitzer error: ", "mst", err.Error())
			return
		}

		if d.playoutTs != pkt.Timestamp {
			d.playoutTs = pkt.Timestamp
			d.frameBuffer = d.frameBuffer[:0] // drop old data
			droppingFrame = false
		}

		if d.missedPacketTime != nil && !d.fastSkip {
			slog.Info("got packet before timeout", "seqnr", pkt.SequenceNumber)
			d.missedPacketTime = nil
		}

		// log packet
		slog.Info("rtp to pts mapping",
			"rtp-timestamp", pkt.Timestamp,
			"sequence-number", pkt.Header.SequenceNumber,
			"unwrapped-sequence-number", d.unwrapper.Unwrap(pkt.Header.SequenceNumber),
			"pts", pkt.Timestamp, // should be fine to use rtp ts as pts
		)

		var payload []byte
		switch d.codec {
		case codec.VP8:
			payload, err = d.vp8Depacketizer.Unmarshal(pkt.Payload)
			if err != nil {
				panic(err)
			}
		case codec.VP9:
			payload, err = d.vp9Depacketizer.Unmarshal(pkt.Payload)
			if err != nil {
				panic(err)
			}
		case codec.H264:
			payload, err = d.h264Depacketizer.Unmarshal(pkt.Payload)
			if err != nil {
				panic(err)
			}
		}

		d.frameBuffer = append(d.frameBuffer, payload...)

		// end of frame
		if pkt.Marker && !droppingFrame {
			frame := make([]byte, len(d.frameBuffer))
			copy(frame, d.frameBuffer)
			d.onFrame(frame, int64(pkt.Timestamp))
		}
	}
}

func (d *rtpDepacketizer) Close() error {
	d.cancel()
	return nil
}

// RTPDepacketizer is a linkable depacketizer element
type RTPDepacketizer struct {
	depacketizer *rtpDepacketizer
	next         Writer
}

func NewRTPDepacketizer(timeout time.Duration, codec codec.CodecType) (*RTPDepacketizer, error) {
	adapter := &RTPDepacketizer{}

	// forwards to next writer when frame is complete
	var err error
	adapter.depacketizer, err = newRTPDepacketizer(timeout, codec, func(frame []byte, pts int64) {
		if adapter.next != nil {
			// Forward the assembled frame to the next stage
			adapter.next.Write(frame, Attributes{PTS: pts})
		} else {
			panic("RTPDepacketizer: used before linked")
		}
	})

	return adapter, err
}

func (d *RTPDepacketizer) Link(next Writer, i Info) (Writer, error) {
	d.next = next

	go d.depacketizer.Run()

	return WriterFunc(func(rtpPacket []byte, attrs Attributes) error {
		return d.depacketizer.Write(rtpPacket)
	}), nil
}

func (d *RTPDepacketizer) UpdateRTT(rtt time.Duration) {
	d.depacketizer.UpdateRTT(rtt)
}

func (d *RTPDepacketizer) Close() error {
	return d.depacketizer.Close()
}
