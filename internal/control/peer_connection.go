package control

import "github.com/mengelbart/mrtp/webrtc"

type Signaler interface{}

type PeerConnection struct {
	ID        SessionID
	transport *webrtc.Transport

	signaler Signaler
}

func NewPeerConnection() (*PeerConnection, error) {
	return &PeerConnection{}, nil
}

func (p *PeerConnection) SetSignaler(s Signaler) {
	p.signaler = s
}

func (p *PeerConnection) HandleMessage() {}
