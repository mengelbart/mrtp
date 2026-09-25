package main

import (
	"context"
	"errors"
	"log/slog"

	"github.com/mengelbart/mrtp/signaling"
	"github.com/mengelbart/mrtp/webrtc"
)

// offerTrickle opens a trickle ICE session on the answerer's signaling server
// and exchanges candidates in the background until either side has sent all
// of its own or ctx is done. source names the file the answerer sends, empty
// for fake media. It returns the session id.
func offerTrickle(ctx context.Context, signaler *signaling.Client, transport *webrtc.Transport, source string) (string, error) {
	offer, err := transport.Offer(ctx)
	if err != nil {
		return "", err
	}
	session, err := signaler.Open(ctx, signaling.Request{
		Protocol: signaling.ProtocolWebRTC,
		Source:   source,
		WebRTC:   &signaling.WebRTCOffer{SDP: offer, Trickle: true},
	})
	if err != nil {
		return "", err
	}
	if session.WebRTC == nil {
		closeSession(signaler, session.ID)
		return "", errors.New("session response has no WebRTC answer")
	}
	if err = transport.SetAnswer(session.WebRTC.SDP); err != nil {
		closeSession(signaler, session.ID)
		return "", err
	}
	go func() {
		if sendErr := signaler.SendCandidates(ctx, session.ID, transport.LocalCandidates()); sendErr != nil && ctx.Err() == nil {
			slog.Error("failed to send ICE candidates", "error", sendErr)
		}
	}()
	go func() {
		readErr := signaler.ReadCandidates(ctx, session.ID, transport.AddICECandidate)
		if readErr != nil && ctx.Err() == nil {
			slog.Error("failed to read ICE candidates", "error", readErr)
		}
	}()
	return session.ID, nil
}
