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

// rtpReadBufferSize is the size of the buffer one received RTP packet is read
// into.
const rtpReadBufferSize = math.MaxUint16

// rtpSink wraps a transport's outgoing RTP endpoint as the element a media
// pipeline writes into. The pipeline closes it, which closes w.
func rtpSink(w io.WriteCloser) mrtp.Sink[mrtp.RTPPacket] {
	return pipeline.SinkFromWriter(w, rtpBytes)
}

// rtpSource wraps a transport's incoming RTP endpoint as the element a media
// pipeline reads from. The pipeline drives it and closes it, which closes r.
func rtpSource(r io.ReadCloser, config media.ReceiverConfig) (mrtp.Source[mrtp.RTPPacket], error) {
	format, err := media.RTPFormat(config.Codec, config.PayloadType)
	if err != nil {
		return nil, err
	}
	return pipeline.SourceFromReader(r, format, rtpReadBufferSize, rtpBytes), nil
}

// rtpBytes says where an RTP packet keeps its buffer, for the io adapters.
func rtpBytes(p *mrtp.RTPPacket) *[]byte {
	return &p.Data
}

// quicRTT reports a QUIC connection's round trip time as an mrtp.RTTSource, so
// a pipeline can scale how long it waits for a missing packet.
type quicRTT struct{ *quictransport.Transport }

func (r quicRTT) RTT() time.Duration { return r.GetRTT() }
