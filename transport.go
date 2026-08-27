package mrtp

import "time"

// RTTSource is implemented by a transport endpoint that knows the round trip
// time of the connection.
//
// It is optional: a media pipeline that adapts to the RTT, such as one deciding
// how long to wait for a missing packet, type-asserts its endpoint to it and
// falls back to a fixed value for a transport that does not know its RTT.
type RTTSource interface {
	// RTT returns the current round trip time of the connection.
	RTT() time.Duration
}
