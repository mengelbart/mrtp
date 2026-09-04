// Package media defines the boundary between media pipelines and transports.
//
// A media pipeline (GStreamer, gopipe, ...) produces and consumes RTP packets
// and a transport (RoQ, WebRTC, UDP, ...) moves them. Neither knows the other's
// type: a pipeline hands out its ports and the caller wires the transport to
// them.
//
// The package stays free of cgo, so that command wiring compiles without
// GStreamer or the native codecs present.
package media

import (
	"errors"
	"fmt"
	"math"

	"github.com/mengelbart/mrtp"
	"github.com/mengelbart/mrtp/pipeline"
)

// Reserved media locations. Only SinkDiscard is a reserved word: every other
// non-empty location is a file, so a file named "discard" needs a path
// ("./discard").
const (
	// SourceTest asks for the pipeline's generated test source. Not every
	// pipeline has one.
	SourceTest = ""
	// SinkDisplay asks for the media to be rendered. Not every pipeline can.
	SinkDisplay = ""
	// SinkDiscard drops the media once it is decoded.
	SinkDiscard = "discard"
)

// RateBounds are the congestion controller's bitrate bounds, in bits per
// second. Only a pipeline that runs its own congestion control needs them
// (GStreamer's screamtx does).
type RateBounds struct {
	Initial uint
	Min     uint
	Max     uint
}

// SenderConfig describes one outgoing stream.
type SenderConfig struct {
	// Name identifies the stream in logs and in element names.
	Name string

	Codec       mrtp.Codec
	PayloadType int

	// SourceLocation is the file to read media from, or SourceTest.
	SourceLocation string

	RateBounds RateBounds
}

// ReceiverConfig describes one incoming stream.
type ReceiverConfig struct {
	// Name identifies the stream in logs and in element names.
	Name string

	Codec       mrtp.Codec
	PayloadType int

	// SinkLocation is the file to write media to, SinkDisplay or SinkDiscard.
	SinkLocation string

	// RTT is the round trip time of the transport carrying the stream, or nil
	// for a transport that does not know it.
	RTT mrtp.RTTSource
}

// RTPFormat is the format of the edge carrying one stream's packets.
func RTPFormat(c mrtp.Codec, payloadType int) (mrtp.RTP, error) {
	if payloadType < 0 || payloadType > math.MaxInt8 {
		return mrtp.RTP{}, fmt.Errorf("invalid payload type %v: the RTP payload type field is 7 bits, so it must be in [0, %v]", payloadType, math.MaxInt8)
	}
	return mrtp.RTP{
		Codec:       c,
		PayloadType: uint8(payloadType),
		ClockRate:   uint32(c.ClockRate()),
	}, nil
}

// Sender steers a running outgoing stream.
type Sender interface {
	// SetTargetBitrate sets the encoder's target bitrate in bits per second. A
	// pipeline whose source cannot adapt does nothing and returns nil.
	SetTargetBitrate(uint) error
}

// SendStream is the pipeline's side of one outgoing stream.
type SendStream struct {
	// Graph holds the stream's elements. The caller wires its transport into
	// it and hands it to a [pipeline.Runner], which then owns everything in
	// it. It is empty for a pipeline that moves the packets itself.
	Graph *pipeline.Graph

	// RTP carries the packets to send, nil if the pipeline sends them itself.
	RTP mrtp.Source[mrtp.RTPPacket]
	// RTCP carries the RTCP the pipeline generates, nil if it generates none.
	RTCP mrtp.Source[mrtp.RTCPPacket]
	// Feedback takes the peer's RTCP, nil if the pipeline has no use for it.
	Feedback mrtp.Consumer[mrtp.RTCPPacket]

	// Sender steers the encoder's bitrate.
	Sender Sender
}

// ConnectRTP wires the stream to the transport endpoint that sends its packets.
func (s *SendStream) ConnectRTP(sink mrtp.Sink[mrtp.RTPPacket]) error {
	if s.RTP == nil {
		return errors.New("the pipeline sends the RTP packets itself, it has no RTP port")
	}
	return s.Graph.Connect(s.RTP, sink)
}

// ConnectRTCP wires the stream to a transport's RTCP endpoints. Either may be
// nil, and a direction the pipeline does not have leaves its endpoint
// unconnected but owned by the graph, so that it is still closed.
//
// recv pulls because a transport may have to keep reading whether or not the
// pipeline consumes the result, such as WebRTC pumping its interceptor chain.
func (s *SendStream) ConnectRTCP(send mrtp.Sink[mrtp.RTCPPacket], recv mrtp.Puller[mrtp.RTCPPacket]) error {
	return connectRTCP(s.Graph, s.RTCP, s.Feedback, send, recv)
}

// ReceiveStream is the pipeline's side of one incoming stream.
type ReceiveStream struct {
	// Graph holds the stream's elements, see [SendStream.Graph].
	Graph *pipeline.Graph

	// RTP takes the packets received from the peer, nil if the pipeline
	// receives them itself.
	RTP mrtp.Sink[mrtp.RTPPacket]
	// RTCP carries the RTCP the pipeline generates, nil if it generates none.
	RTCP mrtp.Source[mrtp.RTCPPacket]
	// Feedback takes the peer's RTCP, nil if the pipeline has no use for it.
	Feedback mrtp.Consumer[mrtp.RTCPPacket]
}

// ConnectRTP wires the stream to the transport endpoint that delivers its
// packets.
func (s *ReceiveStream) ConnectRTP(source mrtp.Source[mrtp.RTPPacket]) error {
	if s.RTP == nil {
		return errors.New("the pipeline receives the RTP packets itself, it has no RTP port")
	}
	return s.Graph.Connect(source, s.RTP)
}

// ConnectRTCP wires the stream to a transport's RTCP endpoints, see
// [SendStream.ConnectRTCP].
func (s *ReceiveStream) ConnectRTCP(send mrtp.Sink[mrtp.RTCPPacket], recv mrtp.Puller[mrtp.RTCPPacket]) error {
	return connectRTCP(s.Graph, s.RTCP, s.Feedback, send, recv)
}

func connectRTCP(
	g *pipeline.Graph,
	out mrtp.Source[mrtp.RTCPPacket], in mrtp.Consumer[mrtp.RTCPPacket],
	send mrtp.Sink[mrtp.RTCPPacket], recv mrtp.Puller[mrtp.RTCPPacket],
) error {
	var errs []error
	switch {
	case send == nil && out != nil:
		errs = append(errs, errors.New("the pipeline generates RTCP, but the transport has no endpoint to send it on"))
	case send == nil:
	case out == nil:
		g.Add(send)
	default:
		errs = append(errs, g.Connect(out, send))
	}
	switch {
	case recv == nil && in != nil:
		errs = append(errs, errors.New("the pipeline expects RTCP, but the transport has no endpoint to receive it on"))
	case recv == nil:
	case in == nil:
		g.Add(recv)
	default:
		errs = append(errs, g.Attach(recv, in))
	}
	return errors.Join(errs...)
}

// Factory builds the pipeline's side of the streams it carries. One factory can
// carry several streams in both directions, because some implementations
// (notably GStreamer's rtpbin) share state between them.
//
// A stream may be built while the runner carrying it is already running: WebRTC
// only learns about an incoming stream once the remote track arrives.
type Factory interface {
	// Shared is the graph carrying whatever outlives a single stream, such as
	// GStreamer's rtpbin and its main loop. It is empty for an implementation
	// with no shared state.
	Shared() *pipeline.Graph

	NewSender(SenderConfig) (*SendStream, error)
	NewReceiver(ReceiverConfig) (*ReceiveStream, error)
}
