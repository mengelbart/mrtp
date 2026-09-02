package mrtp

import "time"

// A Packet is one owned reference to a value of type T. [Sink.Write] and
// [Puller.Pull] transfer ownership, so the receiver must either Release the
// packet or pass it downstream. Clone first to keep or fan out a packet.
type Packet[T any] interface {
	// Value is valid until Release.
	Value() *T

	// Clone returns an independent reference, which the caller must Release.
	Clone() Packet[T]

	// Release gives the reference up.
	Release()
}

// RawFrame is one decoded picture, its planes pointing into pooled buffers.
type RawFrame struct {
	Y, Cb, Cr []byte

	PTS      time.Duration
	Duration time.Duration
}

// EncodedFrame is one coded picture.
type EncodedFrame struct {
	Data []byte

	PTS      time.Duration
	Duration time.Duration

	Keyframe bool
}

// RTPPacket is one marshalled RTP packet.
type RTPPacket struct {
	Data []byte
}

// Marker reports the RTP marker bit, which ends an access unit.
func (p *RTPPacket) Marker() bool {
	return len(p.Data) > 1 && p.Data[1]&0x80 != 0
}

// DataChunk is non-media payload, such as a data channel's.
type DataChunk struct {
	Data []byte
}
