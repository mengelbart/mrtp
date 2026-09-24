// Package signaling defines the HTTP signaling protocol between an mrtp client
// and server.
package signaling

const (
	ProtocolRTPUDP = "rtp-udp"
	ProtocolWebRTC = "webrtc"
)

// Request is the body of POST /sessions.
type Request struct {
	Protocol string       `json:"protocol"`
	WebRTC   *WebRTCOffer `json:"webrtc,omitempty"`
}

// Response is the body of a successful POST /sessions.
type Response struct {
	ID     string        `json:"id"`
	RTP    *RTPEndpoint  `json:"rtp,omitempty"`
	WebRTC *WebRTCAnswer `json:"webrtc,omitempty"`
}

// RTPEndpoint is where the client sends RTP packets.
type RTPEndpoint struct {
	// Address is the server's UDP address in host:port form.
	Address string `json:"address"`
}

type WebRTCOffer struct {
	SDP string `json:"sdp"`
}

type WebRTCAnswer struct {
	SDP string `json:"sdp"`
}
