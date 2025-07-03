package webrtc

import (
	"errors"
	"log/slog"

	"github.com/pion/interceptor"
	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v4"
)

type Signaler interface {
	SendSessionDescription(*webrtc.SessionDescription) error
	SendICECandidate(*webrtc.ICECandidate) error
}

type Transport struct {
	logger   *slog.Logger
	pc       *webrtc.PeerConnection
	signaler Signaler
	offerer  bool

	onRemoteTrack func(*RTPReceiver)
}

type Option func(*Transport) error

func OnTrack(handler func(*RTPReceiver)) Option {
	return func(t *Transport) error {
		t.onRemoteTrack = handler
		return nil
	}
}

func NewTransport(signaler Signaler, offerer bool, opts ...Option) (*Transport, error) {
	se := webrtc.SettingEngine{}
	me := &webrtc.MediaEngine{}
	if err := me.RegisterDefaultCodecs(); err != nil {
		return nil, err
	}
	ir := &interceptor.Registry{}
	// if err := webrtc.RegisterDefaultInterceptors(me, ir); err != nil {
	// 	return nil, err
	// }
	p, err := webrtc.NewAPI(
		webrtc.WithSettingEngine(se),
		webrtc.WithMediaEngine(me),
		webrtc.WithInterceptorRegistry(ir),
	).NewPeerConnection(webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{
				URLs: []string{"stun:stun.l.google.com:19302"},
			},
		},
	})
	if err != nil {
		return nil, err
	}
	t := &Transport{
		logger:        slog.Default(),
		pc:            p,
		signaler:      signaler,
		offerer:       offerer,
		onRemoteTrack: nil,
	}
	for _, opt := range opts {
		if err := opt(t); err != nil {
			return nil, err
		}
	}
	p.OnNegotiationNeeded(t.onNegotiationNeeded)
	p.OnICECandidate(t.onICECandidate)
	p.OnTrack(t.onTrack)
	p.OnConnectionStateChange(func(pcs webrtc.PeerConnectionState) {
		t.logger.Info("connection state changed", "new_state", pcs)
	})
	return t, nil
}

func (t *Transport) onNegotiationNeeded() {
	t.logger.Info("peer connection needs negotiation")
	if t.offerer {
		t.logger.Info("creating offer")
		offer, err := t.pc.CreateOffer(nil)
		if err != nil {
			t.logger.Error("failed to create offer", "error", err)
			return
		}
		if err = t.pc.SetLocalDescription(offer); err != nil {
			t.logger.Error("failed to set local description", "error", err)
			return
		}
		if err = t.signaler.SendSessionDescription(&offer); err != nil {
			t.logger.Error("signaler failed to send session description", "error", err)
			return
		}
	}
}

func (t *Transport) onICECandidate(i *webrtc.ICECandidate) {
	t.logger.Info("got new ICE candidate", "candidate", i)
	if i == nil {
		return
	}
	if err := t.signaler.SendICECandidate(i); err != nil {
		t.logger.Error("signaler failed to send ICE candidate", "error", err)
		return
	}
}

func (t *Transport) onTrack(tr *webrtc.TrackRemote, r *webrtc.RTPReceiver) {
	t.logger.Info("got new track")
	if t.onRemoteTrack != nil {
		t.onRemoteTrack(&RTPReceiver{
			track:    tr,
			receiver: r,
		})
	}
}

func (t *Transport) HandleSessionDescription(description *webrtc.SessionDescription) error {
	if t.offerer && description.Type == webrtc.SDPTypeOffer {
		t.logger.Error("got remote offer but also acting as offerer")
		return errors.New("can't accept your offer since I'm an offerer myself")
	}
	if err := t.pc.SetRemoteDescription(*description); err != nil {
		t.logger.Error("failed to set remote description", "error", err)
		return errors.New("failed to process session description")
	}
	if description.Type != webrtc.SDPTypeOffer {
		return nil
	}

	answer, err := t.pc.CreateAnswer(nil)
	if err != nil {
		t.logger.Error("failed to create answer", "error", err)
		return errors.New("failed to create answer")
	}
	if err = t.pc.SetLocalDescription(answer); err != nil {
		t.logger.Error("failed to set answer as local description", "error", err)
		return errors.New("failed to set local description")
	}
	if err = t.signaler.SendSessionDescription(t.pc.LocalDescription()); err != nil {
		t.logger.Error("signaler failed to send session description", "error", err)
		return errors.New("failed to send answer")
	}
	return nil
}

func (t *Transport) HandleICECandidate(candidate webrtc.ICECandidateInit) error {
	return t.pc.AddICECandidate(candidate)
}

func (t *Transport) AddRemoteVideoTrack() error {
	t.logger.Info("adding video transceiver")
	_, err := t.pc.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo)
	return err
}

func (t *Transport) AddLocalTrack() (*RTPSender, error) {
	track, err := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{
		MimeType:     webrtc.MimeTypeH264,
		ClockRate:    0,
		Channels:     0,
		SDPFmtpLine:  "",
		RTCPFeedback: []webrtc.RTCPFeedback{},
	}, "video", "pion")
	if err != nil {
		return nil, err
	}
	sender, err := t.pc.AddTrack(track)
	if err != nil {
		return nil, err
	}
	return &RTPSender{
		track:  track,
		sender: sender,
	}, nil
}

// Write sends an RTCP packet to the peer
func (t *Transport) Write(pkt []byte) (int, error) {
	pkts, err := rtcp.Unmarshal(pkt)
	if err != nil {
		return 0, err
	}
	return len(pkt), t.pc.WriteRTCP(pkts)
}

func (t *Transport) Close() error {
	return t.pc.Close()
}
