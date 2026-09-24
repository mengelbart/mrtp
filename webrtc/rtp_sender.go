package webrtc

import (
	"fmt"

	"github.com/mengelbart/mrtp"
	"github.com/pion/webrtc/v4"
)

// RTPSender sends the RTP of one local track, one packet per Write.
type RTPSender struct {
	track  *webrtc.TrackLocalStaticRTP
	sender *webrtc.RTPSender
	rtcp   *RTCPReceiver
}

// Negotiate implements mrtp.Sink. Only the codec is checked: Pion overwrites
// the payload type and the SSRC of every packet with what it negotiated, so
// what the edge carries in those fields is not what goes on the wire. The
// payload bytes are passed through untouched, so a codec the track was not
// built for would reach the peer under the negotiated codec's payload type.
func (s *RTPSender) Negotiate(f mrtp.Format) error {
	rtp, ok := f.(mrtp.RTP)
	if !ok {
		return fmt.Errorf("webrtc: rtp sender takes an RTP format, got %v", f)
	}
	codec, err := mrtp.NewCodecFromMimeType(s.track.Codec().MimeType)
	if err != nil {
		return err
	}
	if rtp.Codec != codec {
		return fmt.Errorf("webrtc: track carries %v, cannot send %v", codec, rtp.Codec)
	}
	return nil
}

// Write implements mrtp.Sink, sending one RTP packet.
func (s *RTPSender) Write(p mrtp.Packet[mrtp.RTPPacket]) error {
	defer p.Release()
	_, err := s.track.Write(p.Value().Data)
	return err
}

// EndOfStream implements mrtp.Sink. A track outlives the stream that ended,
// and is stopped rather than ended.
func (s *RTPSender) EndOfStream() error {
	return nil
}

// Close implements mrtp.Element.
func (s *RTPSender) Close() error {
	return s.sender.Stop()
}

// RTCPReceiver is the RTCP the peer sends about this track. Every call returns
// the same receiver.
func (s *RTPSender) RTCPReceiver() *RTCPReceiver {
	return s.rtcp.want()
}

var _ mrtp.Sink[mrtp.RTPPacket] = (*RTPSender)(nil)
