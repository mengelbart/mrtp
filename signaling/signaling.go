// Package signaling defines the HTTP signaling protocol between an mrtp client
// and server.
package signaling

const (
	ProtocolRTPUDP = "rtp-udp"
	ProtocolWebRTC = "webrtc"
)

// Directions of an RTP over UDP session, from the client's view.
const (
	DirectionSend = "send"
	DirectionRecv = "recv"
)

// Request is the body of POST /sessions.
type Request struct {
	Protocol string `json:"protocol"`
	// Source names the media file the server sends. Empty asks for fake
	// media.
	Source string       `json:"source,omitempty"`
	RTP    *RTPRequest  `json:"rtp,omitempty"`
	WebRTC *WebRTCOffer `json:"webrtc,omitempty"`
}

// Response is the body of a successful POST /sessions.
type Response struct {
	ID     string        `json:"id"`
	RTP    *RTPEndpoint  `json:"rtp,omitempty"`
	WebRTC *WebRTCAnswer `json:"webrtc,omitempty"`
}

type RTPRequest struct {
	Direction string `json:"direction"`
	// Address is where the client receives RTP packets in host:port form. It
	// is required for DirectionRecv.
	Address string `json:"address,omitempty"`
}

// RTPEndpoint is the server's end of an RTP over UDP session.
type RTPEndpoint struct {
	// Address is the UDP address in host:port form the server receives on, or
	// sends from.
	Address string `json:"address"`
	// Codec and PayloadType describe what the server sends. They are empty if
	// the server receives.
	Codec       string `json:"codec,omitempty"`
	PayloadType uint8  `json:"payloadType,omitempty"`
}

type WebRTCOffer struct {
	SDP     string `json:"sdp"`
	Trickle bool   `json:"trickle,omitempty"`
}

type WebRTCAnswer struct {
	SDP string `json:"sdp"`
}

// ICECandidate is the JSON form of an ICE candidate, as the browser's
// RTCIceCandidate.toJSON returns it.
type ICECandidate struct {
	Candidate        string  `json:"candidate"`
	SDPMid           *string `json:"sdpMid"`
	SDPMLineIndex    *uint16 `json:"sdpMLineIndex"`
	UsernameFragment *string `json:"usernameFragment"`
}
