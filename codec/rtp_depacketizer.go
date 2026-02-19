package codec

import (
	"context"
	"log/slog"
	"time"

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
	timeout          time.Duration

	unwrapper *logging.Unwrapper // for logging the rtp packets
}

func newRTPDepacketizer(timeout time.Duration, onFrame func(encFrame []byte, pts int64)) *rtpDepacketizer {
	ctx, cancel := context.WithCancel(context.Background())
	d := &rtpDepacketizer{
		jitterBuffer: jitterbuffer.New(),
		frameBuffer:  make([]byte, 0, 2000),
		onFrame:      onFrame,
		ctx:          ctx,
		cancel:       cancel,
		trigger:      make(chan struct{}, 1),
		timeout:      timeout,
		unwrapper:    &logging.Unwrapper{},
	}
	return d
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
			// buffer is empty - wait for more packets
			return
		}

		pkt, err := d.jitterBuffer.Pop()
		if err == jitterbuffer.ErrPopWhileBuffering {
			// still buffering - wait for more packets
			return
		}
		if err == jitterbuffer.ErrNotFound {
			slog.Info("packet reordering")
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
			slog.Error("Depackitzer error: ", "mst", err.Error())
			return
		}

		// log packet
		slog.Info("rtp to pts mapping",
			"rtp-timestamp", pkt.Timestamp,
			"sequence-number", pkt.Header.SequenceNumber,
			"unwrapped-sequence-number", d.unwrapper.Unwrap(pkt.Header.SequenceNumber),
			"pts", pkt.Timestamp, // should be fine to use rtp ts as pts
		)

		var vp8 codecs.VP8Packet
		payload, err := vp8.Unmarshal(pkt.Payload)
		if err != nil {
			panic(err)
		}

		// start of frame: RFC 7742: "The S bit MUST be set to 1 for the first packet of each encoded frame."
		if vp8.S == 1 {
			d.frameBuffer = d.frameBuffer[:0] // drop old data
			droppingFrame = false
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

func NewRTPDepacketizer(timeout time.Duration) *RTPDepacketizer {
	adapter := &RTPDepacketizer{}

	// forwards to next writer when frame is complete
	adapter.depacketizer = newRTPDepacketizer(timeout, func(frame []byte, pts int64) {
		if adapter.next != nil {
			// Forward the assembled frame to the next stage
			adapter.next.Write(frame, Attributes{PTS: pts})
		} else {
			panic("RTPDepacketizer: used before linked")
		}
	})

	return adapter
}

func (d *RTPDepacketizer) Link(next Writer, i Info) (Writer, error) {
	d.next = next

	go d.depacketizer.Run()

	return WriterFunc(func(rtpPacket []byte, attrs Attributes) error {
		return d.depacketizer.Write(rtpPacket)
	}), nil
}

func (d *RTPDepacketizer) Close() error {
	return d.depacketizer.Close()
}
