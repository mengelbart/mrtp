package codec

import (
	"context"

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
}

func NewRTPDepacketizer(onFrame func([]byte)) *RTPDepacketizer {
	ctx, cancel := context.WithCancel(context.Background())
	d := &RTPDepacketizer{
		jitterBuffer: jitterbuffer.New(),
		frameBuffer:  make([]byte, 0, 100000),
		onFrame:      onFrame,
		ctx:          ctx,
		cancel:       cancel,
		trigger:      make(chan struct{}, 100),
	}
	go d.run() // Start background processor
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

// run processes packets and assembles frames
func (d *RTPDepacketizer) run() {
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
	for {
		pkt, err := d.jitterBuffer.Pop()
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
		}

		d.frameBuffer = append(d.frameBuffer, payload...)

		// end of frame
		if pkt.Marker {
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
