package webrtc

import (
	"context"
	"errors"
	"io"

	"github.com/mengelbart/mrtp"
	"github.com/mengelbart/mrtp/pipeline"
	"github.com/pion/webrtc/v4"
)

// RTPReceiver receives the RTP of one remote track and pushes it downstream,
// one packet per Write.
type RTPReceiver struct {
	track    *webrtc.TrackRemote
	receiver *webrtc.RTPReceiver
	codec    mrtp.Codec
	format   mrtp.RTP
	pool     *pipeline.Pool[mrtp.RTPPacket]
	down     mrtp.Sink[mrtp.RTPPacket]
}

// newRTPReceiver configures the element from what the peers negotiated for the
// track, which is not necessarily what this peer sends.
func newRTPReceiver(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) (*RTPReceiver, error) {
	codec, err := mrtp.NewCodecFromMimeType(track.Codec().MimeType)
	if err != nil {
		return nil, err
	}
	return &RTPReceiver{
		track:    track,
		receiver: receiver,
		codec:    codec,
		format: mrtp.RTP{
			Codec:       codec,
			PayloadType: uint8(track.PayloadType()),
			ClockRate:   uint32(codec.ClockRate()),
		},
		pool: pipeline.NewPool(
			func() *mrtp.RTPPacket {
				return &mrtp.RTPPacket{Data: make([]byte, packetBufferSize)}
			},
			func(p *mrtp.RTPPacket) {
				p.Data = p.Data[:cap(p.Data)]
			},
		),
	}, nil
}

// Codec is what the peers negotiated for this track.
func (r *RTPReceiver) Codec() mrtp.Codec {
	return r.codec
}

// PayloadType is what the peers negotiated for this track.
func (r *RTPReceiver) PayloadType() uint8 {
	return r.format.PayloadType
}

// Format implements mrtp.Source.
func (r *RTPReceiver) Format() mrtp.Format {
	return r.format
}

// Connect implements mrtp.Source.
func (r *RTPReceiver) Connect(down mrtp.Sink[mrtp.RTPPacket]) error {
	if r.down != nil {
		return errors.New("webrtc: rtp receiver is already connected")
	}
	r.down = down
	return nil
}

// Run implements mrtp.Driver. It reads until the track ends, the track fails,
// or ctx is cancelled.
func (r *RTPReceiver) Run(ctx context.Context) error {
	if r.down == nil {
		return errors.New("webrtc: rtp receiver runs with its output wired")
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		packet := r.pool.Get()
		n, _, err := r.track.Read(packet.Value().Data)
		if err != nil {
			packet.Release()
			if errors.Is(err, io.EOF) {
				return r.down.EndOfStream()
			}
			return err
		}
		packet.Value().Data = packet.Value().Data[:n]
		if err := r.down.Write(packet); err != nil {
			return err
		}
	}
}

// Close implements mrtp.Element.
func (r *RTPReceiver) Close() error {
	return r.receiver.Stop()
}

// RTCPReceiver is the RTCP the peer sends about this track.
func (r *RTPReceiver) RTCPReceiver() *RTCPReceiver {
	return newRTCPReceiver(r.receiver, nil)
}

var (
	_ mrtp.Source[mrtp.RTPPacket] = (*RTPReceiver)(nil)
	_ mrtp.Driver                 = (*RTPReceiver)(nil)
)
