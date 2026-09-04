package subcmd

import (
	"net"
	"strconv"
	"time"

	"github.com/mengelbart/mrtp/internal/quictransport"
)

// The transports for plain RTP, that is for the commands that do not run RTP
// over a connection such as QUIC.
const (
	// transportUDP configures a Go socket.
	transportUDP = "udp"
	// transportGstUDP leaves the packets to the media pipeline's own UDP
	// elements. Only the GStreamer pipeline has them, and it only builds them
	// when it is also given -gst-udp.
	transportGstUDP = "gst-udp"
)

// DefaultTransport is the transport used for plain RTP when the user does not
// choose one.
const DefaultTransport = transportUDP

// transportNames lists the transports for the -transport flag's usage text.
var transportNames = []string{transportUDP, transportGstUDP}

func address(host string, port uint16) string {
	return net.JoinHostPort(host, strconv.Itoa(int(port)))
}

// quicRTT reports a QUIC connection's round trip time as an mrtp.RTTSource, so
// a pipeline can scale how long it waits for a missing packet.
type quicRTT struct{ *quictransport.Transport }

func (r quicRTT) RTT() time.Duration { return r.GetRTT() }
