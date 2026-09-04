package webrtc

import (
	"github.com/mengelbart/mrtp"
	"github.com/pion/rtcp"
)

// RTCPSender is the element a pipeline writes the RTCP it generates into,
// sending one packet per Write. It belongs to the connection rather than to
// one stream, so several streams may have their own.
type RTCPSender struct {
	transport *Transport
}

// Negotiate implements mrtp.Sink. It configures nothing.
func (s *RTCPSender) Negotiate(mrtp.Format) error {
	return nil
}

// Write implements mrtp.Sink, sending one RTCP packet.
func (s *RTCPSender) Write(p mrtp.Packet[mrtp.RTCPPacket]) error {
	defer p.Release()
	pkts, err := rtcp.Unmarshal(p.Value().Data)
	if err != nil {
		return err
	}
	return s.transport.pc.WriteRTCP(pkts)
}

// EndOfStream implements mrtp.Sink.
func (s *RTCPSender) EndOfStream() error {
	return nil
}

// Close implements mrtp.Element. The connection outlives the stream whose RTCP
// this carried, so closing it is the caller's business, not the pipeline's.
func (s *RTCPSender) Close() error {
	return nil
}

var _ mrtp.Sink[mrtp.RTCPPacket] = (*RTCPSender)(nil)
