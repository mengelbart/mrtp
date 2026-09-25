package server

import (
	"errors"
	"fmt"
	"log/slog"
	"net"

	"github.com/mengelbart/mrtp"
	"github.com/mengelbart/mrtp/signaling"
	"github.com/mengelbart/mrtp/udp"
)

// newRTPUDPSession opens one RTP over UDP stream on a socket bound on host.
// The server either receives from the client, or sends fake video or source
// to it. It returns the server's end of the stream.
func newRTPUDPSession(id, host string, request signaling.RTPRequest, source string, m *media, logger *slog.Logger) (*session, *signaling.RTPEndpoint, error) {
	sess := newSession(id, m, logger)
	var endpoint *signaling.RTPEndpoint
	var err error
	switch request.Direction {
	case signaling.DirectionSend:
		if source != "" {
			return nil, nil, fmt.Errorf("%w: source requires direction %q", signaling.ErrBadRequest, signaling.DirectionRecv)
		}
		endpoint, err = receiveRTPUDP(sess, host, request)
	case signaling.DirectionRecv:
		endpoint, err = sendRTPUDP(sess, host, request.Address, source)
	default:
		return nil, nil, fmt.Errorf("%w: unknown direction %q", signaling.ErrBadRequest, request.Direction)
	}
	if err != nil {
		return nil, nil, sess.abort(err)
	}
	sess.start(nil)
	return sess, endpoint, nil
}

// receiveRTPUDP takes the stream request describes on a socket bound on host.
func receiveRTPUDP(sess *session, host string, request signaling.RTPRequest) (*signaling.RTPEndpoint, error) {
	codec, err := mrtp.NewCodec(request.Codec)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid rtp codec %q", signaling.ErrBadRequest, request.Codec)
	}
	format, err := mrtp.NewRTPFormat(codec, int(request.PayloadType))
	if err != nil {
		return nil, fmt.Errorf("%w: invalid rtp format: %w", signaling.ErrBadRequest, err)
	}
	src, err := udp.Listen(net.JoinHostPort(host, "0"), false, format, rtpBytes)
	if err != nil {
		return nil, err
	}
	sess.transport = src
	if err = sess.addReceiver(src, format.Codec); err != nil {
		return nil, err
	}
	return &signaling.RTPEndpoint{Address: src.LocalAddr().String()}, nil
}

// sendRTPUDP sends source from a socket bound on host to address.
func sendRTPUDP(sess *session, host, address, source string) (*signaling.RTPEndpoint, error) {
	if address == "" {
		return nil, fmt.Errorf("%w: missing rtp address", signaling.ErrBadRequest)
	}
	sender, err := sess.media.newSender(source)
	if err != nil {
		return nil, err
	}
	sink, err := udp.DialFrom(address, net.JoinHostPort(host, "0"), false, rtpBytes)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid rtp address: %w", signaling.ErrBadRequest, errors.Join(err, sender.Close()))
	}
	sess.transport = sink
	if _, err = sess.addSender(sender, sink); err != nil {
		return nil, err
	}
	return &signaling.RTPEndpoint{
		Address:     sink.LocalAddr().String(),
		Codec:       sender.Codec.String(),
		PayloadType: mrtp.DefaultPayloadType,
	}, nil
}

func rtpBytes(p *mrtp.RTPPacket) *[]byte { return &p.Data }
