package gopipe

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/mengelbart/mrtp"
	"github.com/mengelbart/mrtp/internal/logging"
	"github.com/mengelbart/mrtp/pipeline"
	"github.com/pion/interceptor/pkg/jitterbuffer"
	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
)

// getDepacketizerByName returns the payload format reader for c, the inverse of
// [getPacketizerByName].
func getDepacketizerByName(c mrtp.Codec) (rtp.Depacketizer, error) {
	switch c {
	case mrtp.VP8:
		return &codecs.VP8Packet{}, nil
	case mrtp.VP9:
		return &codecs.VP9Packet{}, nil
	case mrtp.H264:
		return &codecs.H264Packet{}, nil
	case mrtp.Fake:
		// fake frames carry no media to parse, so their payload passes through
		return fakeDepacketizer{}, nil
	}
	return nil, fmt.Errorf("unknown codec: %v", c)
}

// fakeDepacketizer passes an RTP payload through unchanged.
type fakeDepacketizer struct{}

func (fakeDepacketizer) Unmarshal(payload []byte) ([]byte, error)   { return payload, nil }
func (fakeDepacketizer) IsPartitionHead([]byte) bool                { return true }
func (fakeDepacketizer) IsPartitionTail(marker bool, _ []byte) bool { return marker }

// RTPDepacketizer reassembles encoded frames from the RTP packets it is given,
// ordering them in a jitter buffer on the way. A frame ends at the marker bit,
// or at a change of RTP timestamp when the marker packet is the one that was
// lost.
type RTPDepacketizer struct {
	jitterBuffer *jitterbuffer.JitterBuffer
	frameBuffer  []byte

	depacketizer rtp.Depacketizer
	codec        mrtp.Codec
	clockRate    uint32

	missedPacketTime *time.Time
	// fastSkip skips missing packets immediately after the first timeout,
	// until the buffer drains.
	fastSkip bool
	// droppingFrame suppresses the frame being assembled, because a packet of
	// it was lost or would not parse. It is cleared by the next frame.
	droppingFrame bool
	// playoutTs is the timestamp of the frame being assembled.
	playoutTs uint32
	// lastEmittedTs is the timestamp of the previous frame passed on, and
	// haveLastEmitted says whether there was one.
	lastEmittedTs   uint32
	haveLastEmitted bool

	maxTimeout     time.Duration
	currentTimeout time.Duration
	rtt            mrtp.RTTSource

	pool *pipeline.Pool[mrtp.EncodedFrame]
	down mrtp.Sink[mrtp.EncodedFrame]

	unwrapper *logging.Unwrapper // for logging the rtp packets
}

// NewRTPDepacketizer returns a depacketizer that waits at most maxTimeout for a
// missing packet. How long it is worth waiting depends on the round trip time,
// so rtt steers the wait within that bound and may be nil for a transport that
// does not know its RTT.
func NewRTPDepacketizer(maxTimeout time.Duration, rtt mrtp.RTTSource) *RTPDepacketizer {
	return &RTPDepacketizer{
		jitterBuffer:   jitterbuffer.New(),
		frameBuffer:    make([]byte, 0, 2000),
		maxTimeout:     maxTimeout,
		currentTimeout: maxTimeout,
		rtt:            rtt,
		pool: pipeline.NewPool(
			func() *mrtp.EncodedFrame { return &mrtp.EncodedFrame{} },
			func(f *mrtp.EncodedFrame) { f.Data = f.Data[:0] },
		),
		unwrapper: &logging.Unwrapper{},
	}
}

// Negotiate implements mrtp.Sink.
func (d *RTPDepacketizer) Negotiate(f mrtp.Format) error {
	format, ok := f.(mrtp.RTP)
	if !ok {
		return fmt.Errorf("RTP depacketizer takes RTP, not %v", f)
	}
	depacketizer, err := getDepacketizerByName(format.Codec)
	if err != nil {
		return err
	}
	d.depacketizer = depacketizer
	d.codec = format.Codec
	d.clockRate = format.ClockRate
	return nil
}

// Format implements mrtp.Source. The picture size is not on the wire, so it is
// zero here, and the decoder downstream publishes the size it decodes.
func (d *RTPDepacketizer) Format() mrtp.Format {
	return mrtp.EncodedVideo{Codec: d.codec}
}

// Connect implements mrtp.Source.
func (d *RTPDepacketizer) Connect(down mrtp.Sink[mrtp.EncodedFrame]) error {
	if d.down != nil {
		return errors.New("gopipe: RTP depacketizer is already connected")
	}
	d.down = down
	return nil
}

// Write implements mrtp.Sink. It buffers the packet and passes on every frame
// that the buffer can now complete.
func (d *RTPDepacketizer) Write(packet mrtp.Packet[mrtp.RTPPacket]) error {
	defer packet.Release()

	if d.depacketizer == nil {
		return errors.New("gopipe: RTP depacketizer wrote before it was negotiated")
	}
	if d.rtt != nil {
		d.updateTimeout(d.rtt.RTT())
	}

	// the jitter buffer holds the packet, which outlives the pooled buffer
	pkt := new(rtp.Packet)
	if err := pkt.Unmarshal(bytes.Clone(packet.Value().Data)); err != nil {
		return err
	}
	d.jitterBuffer.Push(pkt)

	return d.processPackets()
}

// updateTimeout scales how long to wait for a missing packet with the round
// trip time, within the configured bound.
func (d *RTPDepacketizer) updateTimeout(rtt time.Duration) {
	if rtt <= 0 {
		return
	}
	d.currentTimeout = min(time.Duration(float64(rtt)*1.5), d.maxTimeout)
}

// processPackets assembles what the jitter buffer can hand out in order, and
// passes on every frame that completes.
func (d *RTPDepacketizer) processPackets() error {
	for {
		_, err := d.jitterBuffer.Peek(true)
		if errors.Is(err, jitterbuffer.ErrBufferUnderrun) {
			// buffer is empty - reset skip state
			if d.fastSkip {
				slog.Info("packetizer fast-skip done, buffer drained")
			}
			d.fastSkip = false
			d.missedPacketTime = nil
			return nil
		}

		pkt, err := d.jitterBuffer.Pop()
		if errors.Is(err, jitterbuffer.ErrPopWhileBuffering) {
			// still buffering - wait for more packets
			return nil
		}
		if errors.Is(err, jitterbuffer.ErrNotFound) {
			if d.skipMissing() {
				continue
			}
			return nil
		}
		if err != nil {
			slog.Error("depacketizer error", "error", err.Error())
			return nil
		}

		if d.playoutTs != pkt.Timestamp {
			// a new frame starts, so whatever is buffered was a frame whose
			// marker packet never arrived
			d.playoutTs = pkt.Timestamp
			d.frameBuffer = d.frameBuffer[:0]
			d.droppingFrame = false
		}

		if d.missedPacketTime != nil && !d.fastSkip {
			slog.Info("got packet before timeout", "seqnr", pkt.SequenceNumber)
			d.missedPacketTime = nil
		}

		slog.Debug("rtp to pts mapping",
			"rtp-timestamp", pkt.Timestamp,
			"sequence-number", pkt.SequenceNumber,
			"unwrapped-sequence-number", d.unwrapper.Unwrap(pkt.SequenceNumber),
			"pts", pkt.Timestamp, // should be fine to use rtp ts as pts
		)

		payload, err := d.depacketizer.Unmarshal(pkt.Payload)
		if err != nil {
			// corrupt payload, drop the frame it belongs to
			slog.Warn("failed to depacketize payload, dropping frame",
				"codec", d.codec.String(),
				"seqnr", pkt.SequenceNumber,
				"error", err,
			)
			d.frameBuffer = d.frameBuffer[:0]
			d.droppingFrame = true
			continue
		}

		d.frameBuffer = append(d.frameBuffer, payload...)

		// end of frame
		if pkt.Marker && !d.droppingFrame {
			if err := d.emit(pkt.Timestamp); err != nil {
				return err
			}
		}
	}
}

// skipMissing decides what to do about a packet the jitter buffer is missing,
// reporting whether to carry on without it.
func (d *RTPDepacketizer) skipMissing() bool {
	playoutHead := d.jitterBuffer.PlayoutHead()

	if d.fastSkip {
		// already timed out once - skip immediately to avoid cascading delay
		slog.Info("packetizer fast-skipping lost packet", "seqnr", playoutHead)

		d.jitterBuffer.SetPlayoutHead(playoutHead + 1)
		d.frameBuffer = d.frameBuffer[:0]
		d.droppingFrame = true
		return true
	}
	if d.missedPacketTime == nil {
		slog.Info("packetizer misses packet; start timeout", "seqnr", playoutHead)

		now := time.Now()
		d.missedPacketTime = &now
		return false
	}
	if time.Since(*d.missedPacketTime) > d.currentTimeout {
		// timeout expired, drop current frame and enter fast-skip mode
		slog.Info("packetizer dropping frame, rtp packet lost", "seqnr", playoutHead)

		d.jitterBuffer.SetPlayoutHead(playoutHead + 1)
		d.frameBuffer = d.frameBuffer[:0]
		d.droppingFrame = true
		d.fastSkip = true
		return true
	}

	// still waiting for missing packet
	return false
}

// emit passes the assembled frame on, timestamped from the RTP clock. The
// duration is the interval since the previous frame, so the first frame of a
// stream reports none. TODO: buffer one frame to calculate its true duration?
func (d *RTPDepacketizer) emit(timestamp uint32) error {
	out := d.pool.Get()
	value := out.Value()
	value.Data = append(value.Data[:0], d.frameBuffer...)
	value.PTS = d.rtpToDuration(timestamp)
	value.Duration = 0
	if d.haveLastEmitted {
		value.Duration = d.rtpToDuration(timestamp - d.lastEmittedTs)
	}
	value.Keyframe = false
	d.lastEmittedTs = timestamp
	d.haveLastEmitted = true

	return d.down.Write(out)
}

// rtpToDuration converts a count of RTP clock ticks to a duration.
func (d *RTPDepacketizer) rtpToDuration(ticks uint32) time.Duration {
	return time.Duration(ticks) * time.Second / time.Duration(d.clockRate)
}

// EndOfStream implements mrtp.Sink.
func (d *RTPDepacketizer) EndOfStream() error {
	return d.down.EndOfStream()
}

// Close implements mrtp.Element.
func (d *RTPDepacketizer) Close() error {
	return nil
}

var (
	_ mrtp.Sink[mrtp.RTPPacket]      = (*RTPDepacketizer)(nil)
	_ mrtp.Source[mrtp.EncodedFrame] = (*RTPDepacketizer)(nil)
	_ rtp.Depacketizer               = fakeDepacketizer{}
)
