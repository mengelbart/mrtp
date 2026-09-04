package subcmd

import (
	"io"
	"math"
	"time"

	"github.com/mengelbart/mrtp"
	"github.com/mengelbart/mrtp/internal/quictransport"
	"github.com/mengelbart/mrtp/media"
	"github.com/mengelbart/mrtp/pipeline"
)

// datagramBufferSize is the size of the buffer one received packet is read
// into. It is the largest datagram a transport can deliver, so that a read
// cannot truncate a packet.
const datagramBufferSize = math.MaxUint16

// rtpSink wraps a transport's outgoing RTP endpoint as the element a media
// pipeline writes into. The pipeline closes it, which closes w.
func rtpSink(w io.WriteCloser) mrtp.Sink[mrtp.RTPPacket] {
	return pipeline.SinkFromWriter(w, mrtp.RTPBytes)
}

// rtpSource wraps a transport's incoming RTP endpoint as the element a media
// pipeline reads from. The pipeline drives it and closes it, which closes r.
func rtpSource(r io.ReadCloser, config media.ReceiverConfig) (mrtp.Source[mrtp.RTPPacket], error) {
	format, err := media.RTPFormat(config.Codec, config.PayloadType)
	if err != nil {
		return nil, err
	}
	return pipeline.SourceFromReader(r, format, datagramBufferSize, mrtp.RTPBytes), nil
}

// rtcpSink wraps a transport's outgoing RTCP endpoint as the element a media
// pipeline writes into. Closing it closes w.
func rtcpSink(w io.WriteCloser) mrtp.Sink[mrtp.RTCPPacket] {
	return pipeline.SinkFromWriter(w, mrtp.RTCPBytes)
}

// quicRTT reports a QUIC connection's round trip time as an mrtp.RTTSource, so
// a pipeline can scale how long it waits for a missing packet.
type quicRTT struct{ *quictransport.Transport }

func (r quicRTT) RTT() time.Duration { return r.GetRTT() }
