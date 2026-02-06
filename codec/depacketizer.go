package codec

import (
	"context"
	"log/slog"
	"time"

	"github.com/pion/interceptor/pkg/jitterbuffer"
	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
)

type RTPDepacketizer struct {
	jitterBuffer *jitterbuffer.JitterBuffer
	frameBuffer  []byte
	onFrame      func([]byte) // callback for complete frames

	ctx     context.Context
	cancel  context.CancelFunc
	trigger chan struct{}

	missedPacketTime *time.Time
	timeout          time.Duration
}

func NewRTPDepacketizer(timeout time.Duration, onFrame func([]byte)) *RTPDepacketizer {
	ctx, cancel := context.WithCancel(context.Background())
	d := &RTPDepacketizer{
		jitterBuffer: jitterbuffer.New(),
		frameBuffer:  make([]byte, 0, 100000),
		onFrame:      onFrame,
		ctx:          ctx,
		cancel:       cancel,
		trigger:      make(chan struct{}, 1),
		timeout:      timeout,
	}
	return d
}

// Write just pushes to jitter buffer
func (d *RTPDepacketizer) Write(rtpBuf []byte) error {
	var pkt rtp.Packet
	if err := pkt.Unmarshal(rtpBuf); err != nil {
		return err
	}

	d.jitterBuffer.Push(&pkt)

	// Signal that new packet is available
	select {
	case d.trigger <- struct{}{}:
	default:
	}

	return nil
}

// Run processes packets and assembles frames
func (d *RTPDepacketizer) Run() {
	for {
		select {
		case <-d.ctx.Done():
			return
		case <-d.trigger:
			d.processPackets()
		}
	}
}

func (d *RTPDepacketizer) processPackets() {
	droppingFrame := false
	for {
		_, err := d.jitterBuffer.Peek(true)
		if err == jitterbuffer.ErrBufferUnderrun {
			// buffer is empty - wait for more packets
			return
		}

		pkt, err := d.jitterBuffer.Pop()
		if err == jitterbuffer.ErrNotFound {
			// missing packet
			if d.missedPacketTime == nil {
				// start new timeout
				now := time.Now()
				d.missedPacketTime = &now
				return
			} else if time.Since(*d.missedPacketTime) > d.timeout {
				// timeout expired, drop current frame
				playoutHead := d.jitterBuffer.PlayoutHead()

				slog.Info("packitzier dropping frame, rtp packet lost", "seqnr", playoutHead)

				d.jitterBuffer.SetPlayoutHead(playoutHead + 1)
				d.frameBuffer = d.frameBuffer[:0]
				droppingFrame = true
				d.missedPacketTime = nil
				continue
			}

			// still waiting for missing packet
			return
		}
		if err != nil {
			return
		}

		var vp8 codecs.VP8Packet
		payload, err := vp8.Unmarshal(pkt.Payload)
		if err != nil {
			panic(err)
		}

		// start of frame
		if vp8.S == 1 {
			d.frameBuffer = d.frameBuffer[:0] // drop old data
			droppingFrame = false
		}

		d.frameBuffer = append(d.frameBuffer, payload...)

		// end of frame
		if pkt.Marker && !droppingFrame {
			frame := make([]byte, len(d.frameBuffer))
			copy(frame, d.frameBuffer)
			d.onFrame(frame)
		}
	}
}

func (d *RTPDepacketizer) Close() error {
	d.cancel()
	return nil
}
