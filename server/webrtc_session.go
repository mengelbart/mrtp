package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"

	"github.com/mengelbart/mrtp/signaling"
	"github.com/mengelbart/mrtp/webrtc"
	"github.com/mengelbart/mrtp/webrtc/ecnnet"
)

const webrtcBufferSize = 10_000_000

// webrtcSession is one WebRTC peer connection. It sends fake video or a
// source file on every recvonly video m-line of the offer, and receives every
// track the client sends. The transport implements signaling.TrickleSession.
type webrtcSession struct {
	*webrtc.Transport
	*session
}

// newWebRTCSession answers offer with a peer connection whose candidates are
// restricted to host. It generates CCFB, NACK and RTCP reports as far as the
// offer negotiates them. Unless the offer trickles ICE, it returns once the
// answer is complete. The pipelines start once the peer connection is up, so
// that nothing is sent before the client can receive it.
func newWebRTCSession(ctx context.Context, id, host string, offer signaling.WebRTCOffer, source string, m *media, logger *slog.Logger) (*webrtcSession, string, error) {
	ip := net.ParseIP(host)
	if ip == nil {
		return nil, "", fmt.Errorf("media host %q is not an IP address", host)
	}
	stdnet, err := ecnnet.New(
		ecnnet.SetRecvBufferSize(webrtcBufferSize),
		ecnnet.TrackECN(true),
	)
	if err != nil {
		return nil, "", err
	}

	sess := &webrtcSession{session: newSession(id, m, logger)}
	options := []webrtc.Option{
		webrtc.SetNet(stdnet),
		webrtc.SetSRTPBufferLimit(webrtcBufferSize),
		webrtc.RegisterDefaultCodecs(),
		webrtc.RegisterFakeCodec(),
		webrtc.EnableCCFB(),
		webrtc.EnableNACK(),
		webrtc.EnableRTCPReports(),
		webrtc.SetICEServers(nil),
		webrtc.IncludeLoopbackCandidates(),
		webrtc.ListenIP(ip),
		webrtc.OnTrack(sess.onTrack),
	}
	if offer.Trickle {
		options = append(options, webrtc.EnableTrickle())
	}
	if sess.Transport, err = webrtc.NewTransport(options...); err != nil {
		return nil, "", sess.abort(err)
	}
	sess.transport = sess.Transport
	if err = sess.SetOffer(offer.SDP); err != nil {
		return nil, "", sess.abort(fmt.Errorf("%w: invalid webrtc offer: %w", signaling.ErrBadRequest, err))
	}
	requested := sess.RequestedVideoTracks()
	if source != "" && requested == 0 {
		return nil, "", sess.abort(fmt.Errorf("%w: source requires a recvonly video m-line", signaling.ErrBadRequest))
	}
	for range requested {
		if err = sess.addTrack(source); err != nil {
			return nil, "", sess.abort(err)
		}
	}
	answer, err := sess.CreateAnswer(ctx)
	if err != nil {
		return nil, "", sess.abort(err)
	}
	sess.start(sess.Connected())
	return sess, answer, nil
}

// addTrack sends source on a new local track.
func (s *webrtcSession) addTrack(source string) error {
	sender, err := s.media.newSender(source)
	if err != nil {
		return err
	}
	track, err := s.AddLocalTrackWithCodec(sender.Codec.MimeType())
	if err != nil {
		return errors.Join(err, sender.Close())
	}
	rate, err := s.addSender(sender, track)
	if err != nil {
		return err
	}
	if rate != nil {
		s.ControlBitrate(rate)
	}
	return nil
}

func (s *webrtcSession) onTrack(receiver *webrtc.RTPReceiver) {
	s.logger.Info("got track", "codec", receiver.Codec())
	if err := s.addReceiver(receiver, receiver.Codec()); err != nil {
		s.logger.Error("failed to receive track", "error", errors.Join(err, receiver.Close()))
	}
}

// Close implements signaling.Session.
func (s *webrtcSession) Close() error {
	return s.session.Close()
}
