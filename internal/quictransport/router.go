package quictransport

import (
	"log/slog"

	"github.com/quic-go/quic-go"
)

// A StreamHandler takes over one unidirectional stream the peer opened.
type StreamHandler func(flowID uint64, rs *quic.ReceiveStream)

// RouteUniStreams installs the handler that gives each unidirectional stream
// the peer opens to the subsystem owning its flow ID: the IDs in claimed go to
// claim, and everything else goes to other.
//
// A stream neither handler takes is cancelled rather than read. The flow ID
// comes from the peer, so an unknown one is a protocol error to be reported,
// not a reason to fail.
func RouteUniStreams(t *Transport, claimed []uint64, claim, other StreamHandler) {
	owned := make(map[uint64]struct{}, len(claimed))
	for _, id := range claimed {
		owned[id] = struct{}{}
	}
	t.HandleUniStream = func(flowID uint64, rs *quic.ReceiveStream) {
		if _, ok := owned[flowID]; ok {
			claim(flowID, rs)
			return
		}
		if other != nil {
			other(flowID, rs)
			return
		}
		slog.Error("unknown stream flow ID, closing stream", "flow-id", flowID)
		rs.CancelRead(0)
	}
}
