package webrtc

import (
	"errors"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/Willi-42/go-nada/nada"
	"github.com/mengelbart/mrtp/logging"
	"github.com/pion/bwe-test/gcc"
	"github.com/pion/interceptor"
	"github.com/pion/interceptor/pkg/packetdump"
	"github.com/pion/interceptor/pkg/rfc8888"
	"github.com/pion/interceptor/pkg/rtpfb"
	"github.com/pion/interceptor/pkg/twcc"
	"github.com/pion/rtcp"
	"github.com/pion/sdp/v2"
	"github.com/pion/transport/v3"
	"github.com/pion/transport/v3/packetio"
	"github.com/pion/webrtc/v4"
)

const (
	// TODO(ME): Make the interval configurable?
	feedbackInterval = 20 * time.Millisecond
)

type Signaler interface {
	SendSessionDescription(*webrtc.SessionDescription) error
	SendICECandidate(*webrtc.ICECandidate) error
}

type Transport struct {
	logger *slog.Logger

	settingEngine       *webrtc.SettingEngine
	mediaEngine         *webrtc.MediaEngine
	interceptorRegistry *interceptor.Registry

	pc       *webrtc.PeerConnection
	signaler Signaler
	offerer  bool

	pendingICECandidatesLock sync.Mutex
	hasRemoteDescription     bool
	pendingICECandidates     []*webrtc.ICECandidate

	onRemoteTrack func(*RTPReceiver)
	onConnected   func()

	bwe           *gcc.SendSideController
	nada          *nada.SenderOnly
	SetTargetRate func(ratebps uint) error
}

type Option func(*Transport) error

func OnTrack(handler func(*RTPReceiver)) Option {
	return func(t *Transport) error {
		t.onRemoteTrack = handler
		return nil
	}
}

func OnConnected(f func()) Option {
	return func(t *Transport) error {
		t.onConnected = f
		return nil
	}
}

func EnableNACK() Option {
	return func(t *Transport) error {
		return webrtc.ConfigureNack(t.mediaEngine, t.interceptorRegistry)
	}
}

func EnableRTCPReports() Option {
	return func(t *Transport) error {
		return webrtc.ConfigureRTCPReports(t.interceptorRegistry)
	}
}

func EnableTWCC() Option {
	return func(t *Transport) error {
		t.mediaEngine.RegisterFeedback(webrtc.RTCPFeedback{Type: webrtc.TypeRTCPFBTransportCC}, webrtc.RTPCodecTypeVideo)
		if err := t.mediaEngine.RegisterHeaderExtension(
			webrtc.RTPHeaderExtensionCapability{URI: sdp.TransportCCURI}, webrtc.RTPCodecTypeVideo,
		); err != nil {
			return err
		}

		t.mediaEngine.RegisterFeedback(webrtc.RTCPFeedback{Type: webrtc.TypeRTCPFBTransportCC}, webrtc.RTPCodecTypeAudio)
		if err := t.mediaEngine.RegisterHeaderExtension(
			webrtc.RTPHeaderExtensionCapability{URI: sdp.TransportCCURI}, webrtc.RTPCodecTypeAudio,
		); err != nil {
			return err
		}

		generator, err := twcc.NewSenderInterceptor(twcc.SendInterval(feedbackInterval))
		if err != nil {
			return err
		}

		t.interceptorRegistry.Add(generator)
		return nil
	}
}

func EnableCCFB() Option {
	return func(t *Transport) error {
		t.mediaEngine.RegisterFeedback(webrtc.RTCPFeedback{Type: webrtc.TypeRTCPFBACK, Parameter: "ccfb"}, webrtc.RTPCodecTypeVideo)
		t.mediaEngine.RegisterFeedback(webrtc.RTCPFeedback{Type: webrtc.TypeRTCPFBACK, Parameter: "ccfb"}, webrtc.RTPCodecTypeAudio)
		generator, err := rfc8888.NewSenderInterceptor(rfc8888.SendInterval(feedbackInterval))
		if err != nil {
			return err
		}
		t.interceptorRegistry.Add(generator)
		return nil
	}
}

func EnableGCC(initRate, minRate, maxRate int) Option {
	return func(t *Transport) error {
		log := &pionLogger{
			sl: slog.Default(),
		}
		var err error
		t.bwe, err = gcc.NewSendSideController(initRate, minRate, maxRate, gcc.Logger(log))
		return err
	}
}

func EnableNADA(initRate, minRate, maxRate uint) Option {
	return func(t *Transport) error {
		nadaConfig := nada.Config{
			MinRate:                  uint64(minRate),
			MaxRate:                  uint64(maxRate),
			StartRate:                uint64(initRate),
			FeedbackDelta:            uint64(feedbackInterval / time.Millisecond), // convert to ms
			DeactivateQDelayWrapping: true,
		}

		nada := nada.NewSenderOnly(nadaConfig)
		t.nada = &nada
		return nil
	}
}

func EnableCCFBReceiver() Option {
	return func(t *Transport) error {
		f, err := rtpfb.NewInterceptor()
		if err != nil {
			return err
		}
		t.interceptorRegistry.Add(f)
		return nil
	}
}

func EnableRTPRecvTraceLogging() Option {
	return func(t *Transport) error {
		f, err := packetdump.NewReceiverInterceptor(packetdump.PacketLog(logging.NewRTPLogger("webrtc-recv", nil)))
		if err != nil {
			return err
		}
		t.interceptorRegistry.Add(f)
		return nil
	}
}

func EnableRTPSendTraceLogging() Option {
	return func(t *Transport) error {
		f, err := packetdump.NewSenderInterceptor(packetdump.PacketLog(logging.NewRTPLogger("webrtc-send", nil)))
		if err != nil {
			return err
		}
		t.interceptorRegistry.Add(f)
		return nil
	}
}

func AddExtraCodecs(name string, clockRate uint32, payloadType uint8) Option {
	return func(t *Transport) error {
		return t.mediaEngine.RegisterCodec(webrtc.RTPCodecParameters{
			RTPCodecCapability: webrtc.RTPCodecCapability{
				MimeType:     name,
				ClockRate:    clockRate,
				Channels:     0,
				SDPFmtpLine:  "",
				RTCPFeedback: []webrtc.RTCPFeedback{},
			},
			PayloadType: webrtc.PayloadType(payloadType),
		}, webrtc.RTPCodecTypeVideo)
	}
}

func RegisterDefaultCodecs() Option {
	return func(t *Transport) error {
		return t.mediaEngine.RegisterDefaultCodecs()
	}
}

func SetNet(net transport.Net) Option {
	return func(t *Transport) error {
		t.settingEngine.SetNet(net)
		return nil
	}
}

func SetSRTPBufferLimit(size int) Option {
	return func(t *Transport) error {
		t.settingEngine.BufferFactory = func(packetType packetio.BufferPacketType, ssrc uint32) io.ReadWriteCloser {
			buffer := packetio.NewBuffer()
			buffer.SetLimitSize(size)
			buffer.SetLimitCount(0)
			return buffer
		}
		return nil
	}
}

func NewTransport(signaler Signaler, offerer bool, opts ...Option) (*Transport, error) {
	t := &Transport{
		logger:              slog.Default(),
		pc:                  nil,
		signaler:            signaler,
		offerer:             offerer,
		onRemoteTrack:       nil,
		settingEngine:       &webrtc.SettingEngine{},
		mediaEngine:         &webrtc.MediaEngine{},
		interceptorRegistry: &interceptor.Registry{},
		SetTargetRate:       nil,
	}
	for _, opt := range opts {
		if err := opt(t); err != nil {
			return nil, err
		}
	}

	pc, err := webrtc.NewAPI(
		webrtc.WithSettingEngine(*t.settingEngine),
		webrtc.WithMediaEngine(t.mediaEngine),
		webrtc.WithInterceptorRegistry(t.interceptorRegistry),
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

	pc.OnNegotiationNeeded(t.onNegotiationNeeded)
	pc.OnICECandidate(t.onICECandidate)
	pc.OnTrack(t.onTrack)
	pc.OnConnectionStateChange(func(pcs webrtc.PeerConnectionState) {
		t.logger.Info("connection state changed", "new_state", pcs)
	})
	pc.OnConnectionStateChange(func(pcs webrtc.PeerConnectionState) {
		if pcs == webrtc.PeerConnectionStateConnected {
			t.onConnected()
		}
	})
	t.pc = pc
	return t, nil
}

func (t *Transport) NewDataChannelSender(label string) *DCsender {
	dc, err := t.pc.CreateDataChannel(label, nil)
	if err != nil {
		panic(err)
	}

	return newDCsender(dc)
}

func (t *Transport) NewDataChannelReceiver() *DCreceiver {
	dcChan := make(chan *webrtc.DataChannel)
	t.pc.OnDataChannel(func(dataChannel *webrtc.DataChannel) {
		dcChan <- dataChannel
	})

	dc := <-dcChan
	return newReceiver(dc)
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
	t.pendingICECandidatesLock.Lock()
	defer t.pendingICECandidatesLock.Unlock()

	if !t.hasRemoteDescription {
		t.pendingICECandidates = append(t.pendingICECandidates, i)
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
	t.pendingICECandidatesLock.Lock()
	defer t.pendingICECandidatesLock.Unlock()
	t.hasRemoteDescription = true
	for _, c := range t.pendingICECandidates {
		if err := t.signaler.SendICECandidate(c); err != nil {
			t.logger.Error("signaler failed to send ICE candidate", "error", err)
		}
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
	return t.addLocalTrackWithCodec(webrtc.MimeTypeH264)
}

func (t *Transport) AddLocalTrackWithCodec(codec string) (*RTPSender, error) {
	return t.addLocalTrackWithCodec(codec)
}

func (t *Transport) addLocalTrackWithCodec(codec string) (*RTPSender, error) {
	track, err := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{
		MimeType:     codec,
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
		onCCFB: t.onCCFB,
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

func (t *Transport) onCCFB(reports []rtpfb.Report) error {
	t.logger.Info("received ccfb packet reports", "length", len(reports))

	var tr uint
	for _, report := range reports {
		// GCC as CC
		if t.bwe != nil {
			acks := []gcc.Acknowledgment{}
			latestAckedArrival := time.Time{}
			latestAckedDeparture := time.Time{}
			for _, prs := range report.SSRCToPacketReports {
				for _, pr := range prs {
					acks = append(acks, gcc.Acknowledgment{
						SeqNr:     pr.SeqNr,
						Size:      uint16(pr.Size),
						Departure: pr.Departure,
						Arrived:   pr.Arrived,
						Arrival:   pr.Arrival,
						ECN:       gcc.ECN(pr.ECN),
					})
					if pr.Arrival.After(latestAckedArrival) {
						latestAckedArrival = pr.Arrival
						latestAckedDeparture = pr.Departure
					}
				}
			}
			rtt := gcc.MeasureRTT(report.Departure, report.Arrival, latestAckedDeparture, latestAckedArrival)
			tr = uint(t.bwe.OnAcks(report.Arrival, rtt, acks))
		}

		// NADA as CC
		if t.nada != nil {
			acks := []nada.Acknowledgment{}
			latestAckedArrival := time.Time{}
			latestAckedDeparture := time.Time{}
			for _, prs := range report.SSRCToPacketReports {
				for _, pr := range prs {
					acks = append(acks, nada.Acknowledgment{
						SeqNr:     pr.SeqNr,
						SizeBit:   uint64(pr.Size * 8),
						Departure: pr.Departure,
						Arrival:   pr.Arrival,
						Arrived:   pr.Arrived,
						Marked:    pr.ECN == rtcp.ECNCE,
					})
					if pr.Arrival.After(latestAckedArrival) {
						latestAckedArrival = pr.Arrival
						latestAckedDeparture = pr.Departure
					}
				}
			}

			rtt := gcc.MeasureRTT(report.Departure, report.Arrival, latestAckedDeparture, latestAckedArrival)
			tr = uint(t.nada.OnAcks(rtt, acks))
		}

		if tr != 0 {
			if t.SetTargetRate != nil {
				// set target rate of encoder
				err := t.SetTargetRate(tr)
				if err != nil {
					return err
				}
			}
		}
	}

	return nil
}
