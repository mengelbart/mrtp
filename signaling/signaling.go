// Package signaling defines the HTTP signaling protocol between an mrtp client
// and server.
//
// A client opens a session with POST /sessions and a JSON [Request]. The server
// answers 201 Created with a JSON [Response] describing where to send media.
// DELETE /sessions/{id} closes the session.
package signaling

// ProtocolRTPUDP asks for plain RTP over UDP from the client to the server.
const ProtocolRTPUDP = "rtp-udp"

// Request is the body of POST /sessions.
type Request struct {
	Protocol string `json:"protocol"`
}

// Response is the body of a successful POST /sessions.
type Response struct {
	ID  string      `json:"id"`
	RTP RTPEndpoint `json:"rtp"`
}

// RTPEndpoint is where the client sends RTP packets.
type RTPEndpoint struct {
	// Address is the server's UDP address in host:port form.
	Address string `json:"address"`
}
