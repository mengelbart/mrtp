package webrtc

import (
	"context"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/mengelbart/mrtp"
	"github.com/mengelbart/mrtp/internal/logging"
	"github.com/pion/interceptor"
	"github.com/pion/interceptor/pkg/pacing"
	"github.com/pion/interceptor/pkg/packetdump"
	"github.com/pion/interceptor/pkg/rfc8888"
	"github.com/pion/interceptor/pkg/rtpfb"
	"github.com/pion/interceptor/pkg/twcc"
	"github.com/pion/sdp/v2"
	"github.com/pion/transport/v4"
	"github.com/pion/transport/v4/packetio"
	"github.com/pion/webrtc/v4"
)

const (
	// TODO(ME): Make the interval configurable?
	feedbackInterval = 20 * time.Millisecond

	// packetBufferSize is the size of the buffer one RTP or RTCP packet is
	// read into. It matches Pion's receive MTU exactly: everything is
	// demultiplexed out of a read into a buffer of that size, and decryption
	// only shrinks a packet, so nothing larger can arrive. A buffer smaller
	// than the packet is reported as io.ErrShortBuffer rather than truncating,
	// so raising the MTU with SettingEngine.SetReceiveMTU means raising this
	// with it.
	packetBufferSize = 1500
)

type Transport struct {
	logger *slog.Logger

	settingEngine       *webrtc.SettingEngine
	mediaEngine         *webrtc.MediaEngine
	interceptorRegistry *interceptor.Registry
	ecnTable            rfc8888.ECNLookupTable

	pc           *webrtc.PeerConnection
	iceServers   []webrtc.ICEServer
	dataChannels chan *webrtc.DataChannel

	onRemoteTrack    func(*RTPReceiver)
	onConnected      func()
	onLocalCandidate func(*webrtc.ICECandidateInit)

	rateController rateController
	pacer          *pacing.InterceptorFactory

	sourceLock sync.Mutex
	source     mrtp.TargetBitrateSetter
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

// OnICECandidate trickles ICE: Offer and CreateAnswer return without waiting for
// candidates, and each local candidate is passed to handler instead, followed
// by nil once gathering is complete.
func OnICECandidate(handler func(*webrtc.ICECandidateInit)) Option {
	return func(t *Transport) error {
		t.onLocalCandidate = handler
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

// EnableCCFB advertises CCFB and sends it.
func EnableCCFB() Option {
	return func(t *Transport) error {
		t.registerCCFB()
		generator, err := rfc8888.NewSenderInterceptor(
			rfc8888.SendInterval(feedbackInterval),
			rfc8888.WithECNLookupTable(ecnLookupFunc(t.getECN)),
		)
		if err != nil {
			return err
		}
		t.interceptorRegistry.Add(generator)
		return nil
	}
}

// SetBWE runs bwe on incoming CCFB reports.
func SetBWE(bwe mrtp.BWE) Option {
	return func(t *Transport) error {
		return t.setRateController(&bweController{bwe: bwe})
	}
}

// EnableCCFBReceiver advertises CCFB and reads incoming reports.
func EnableCCFBReceiver() Option {
	return func(t *Transport) error {
		t.registerCCFB()
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

// videoRTCPFeedback is the feedback Pion registers with its own video codecs.
// A codec added here gets the same, so that it is negotiated like any other.
var videoRTCPFeedback = []webrtc.RTCPFeedback{
	{Type: "goog-remb", Parameter: ""},
	{Type: "ccm", Parameter: "fir"},
	{Type: webrtc.TypeRTCPFBNACK, Parameter: ""},
	{Type: webrtc.TypeRTCPFBNACK, Parameter: "pli"},
}

// AddExtraCodecs registers a video codec Pion does not know, so that it can be
// negotiated and sent on a track.
//
// It has to be applied before the Enable* options: the feedback they register
// is added to the codecs registered so far, so a codec added after them is
// offered without congestion control feedback.
func AddExtraCodecs(name string, clockRate uint32, payloadType uint8) Option {
	return func(t *Transport) error {
		return t.mediaEngine.RegisterCodec(webrtc.RTPCodecParameters{
			RTPCodecCapability: webrtc.RTPCodecCapability{
				MimeType:     name,
				ClockRate:    clockRate,
				Channels:     0,
				SDPFmtpLine:  "",
				RTCPFeedback: videoRTCPFeedback,
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

// SetNet replaces the network stack. A net that implements
// rfc8888.ECNLookupTable, such as ecnnet.Net, supplies the ECN codepoints CCFB
// reports.
func SetNet(net transport.Net) Option {
	return func(t *Transport) error {
		if table, ok := net.(rfc8888.ECNLookupTable); ok {
			t.ecnTable = table
		}
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

func EnablePacing() Option {
	return func(t *Transport) error {
		t.pacer = pacing.NewInterceptor()
		t.interceptorRegistry.Add(t.pacer)
		return nil
	}
}

// SetICEServers replaces the default STUN server.
func SetICEServers(servers []webrtc.ICEServer) Option {
	return func(t *Transport) error {
		t.iceServers = servers
		return nil
	}
}

// ListenIP restricts ICE candidates to ip.
func ListenIP(ip net.IP) Option {
	return func(t *Transport) error {
		t.settingEngine.SetIPFilter(func(candidate net.IP) bool {
			return candidate.Equal(ip)
		})
		return nil
	}
}

// IncludeLoopbackCandidates gathers ICE candidates on loopback interfaces.
func IncludeLoopbackCandidates() Option {
	return func(t *Transport) error {
		t.settingEngine.SetIncludeLoopbackCandidate(true)
		return nil
	}
}

// fakePayloadType is the payload type the FAKE codec is registered under.
const fakePayloadType = 118

// RegisterFakeCodec makes the FAKE codec negotiable.
func RegisterFakeCodec() Option {
	return AddExtraCodecs(mrtp.Fake.MimeType(), uint32(mrtp.Fake.ClockRate()), fakePayloadType)
}

// dataChannelQueueDepth is how many data channels the peer opens are kept
// until NewDataChannelReceiver takes them.
const dataChannelQueueDepth = 4

// NewTransport creates a peer connection. Negotiation is driven by Offer and
// SetAnswer, or by SetOffer and CreateAnswer.
func NewTransport(opts ...Option) (*Transport, error) {
	t := &Transport{
		logger:       slog.Default(),
		dataChannels: make(chan *webrtc.DataChannel, dataChannelQueueDepth),
		iceServers: []webrtc.ICEServer{
			{
				URLs: []string{"stun:stun.l.google.com:19302"},
			},
		},
		settingEngine:       &webrtc.SettingEngine{},
		mediaEngine:         &webrtc.MediaEngine{},
		interceptorRegistry: &interceptor.Registry{},
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
		ICEServers: t.iceServers,
	})
	if err != nil {
		return nil, err
	}

	pc.OnICECandidate(t.onICECandidate)
	pc.OnTrack(t.onTrack)
	pc.OnDataChannel(t.onDataChannel)
	pc.OnConnectionStateChange(func(pcs webrtc.PeerConnectionState) {
		t.logger.Debug("connection state changed", "new_state", pcs)
		if pcs == webrtc.PeerConnectionStateConnected && t.onConnected != nil {
			t.onConnected()
		}
	})
	t.pc = pc
	return t, nil
}

// NewDataChannelSender creates a data channel, which the next offer
// negotiates.
func (t *Transport) NewDataChannelSender(label string) (*DCsender, error) {
	dc, err := t.pc.CreateDataChannel(label, nil)
	if err != nil {
		return nil, err
	}
	return newDCsender(dc), nil
}

// NewDataChannelReceiver returns the next data channel the peer opened,
// blocking until it opens one or ctx is done.
func (t *Transport) NewDataChannelReceiver(ctx context.Context) (*DCreceiver, error) {
	select {
	case dc := <-t.dataChannels:
		return newReceiver(dc), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (t *Transport) onDataChannel(dc *webrtc.DataChannel) {
	select {
	case t.dataChannels <- dc:
	default:
		t.logger.Warn("dropping data channel nothing receives", "label", dc.Label())
	}
}

func (t *Transport) onICECandidate(candidate *webrtc.ICECandidate) {
	t.logger.Info("got new ICE candidate", "candidate", candidate)
	if t.onLocalCandidate == nil {
		return
	}
	if candidate == nil {
		t.onLocalCandidate(nil)
		return
	}
	init := candidate.ToJSON()
	t.onLocalCandidate(&init)
}

func (t *Transport) onTrack(tr *webrtc.TrackRemote, r *webrtc.RTPReceiver) {
	t.logger.Info("got new track")
	if t.onRemoteTrack == nil {
		return
	}
	receiver, err := newRTPReceiver(tr, r)
	if err != nil {
		t.logger.Error("ignoring remote track", "mime-type", tr.Codec().MimeType, "error", err)
		return
	}
	t.onRemoteTrack(receiver)
}

// Offer creates an offer. Without OnICECandidate, it returns once all ICE
// candidates are gathered.
func (t *Transport) Offer(ctx context.Context) (string, error) {
	offer, err := t.pc.CreateOffer(nil)
	if err != nil {
		return "", err
	}
	return t.setLocalDescription(ctx, offer)
}

// SetOffer applies the peer's offer. Local tracks added before CreateAnswer are
// sent on the offer's m-lines that receive.
func (t *Transport) SetOffer(offer string) error {
	return t.pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: offer})
}

// CreateAnswer answers the offer from SetOffer. Without OnICECandidate, it
// returns once all ICE candidates are gathered.
func (t *Transport) CreateAnswer(ctx context.Context) (string, error) {
	answer, err := t.pc.CreateAnswer(nil)
	if err != nil {
		return "", err
	}
	return t.setLocalDescription(ctx, answer)
}

// SetAnswer applies the peer's answer to an offer from Offer.
func (t *Transport) SetAnswer(answer string) error {
	return t.pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: answer})
}

func (t *Transport) setLocalDescription(ctx context.Context, description webrtc.SessionDescription) (string, error) {
	if t.onLocalCandidate != nil {
		if err := t.pc.SetLocalDescription(description); err != nil {
			return "", err
		}
		return t.pc.LocalDescription().SDP, nil
	}
	gathered := webrtc.GatheringCompletePromise(t.pc)
	if err := t.pc.SetLocalDescription(description); err != nil {
		return "", err
	}
	select {
	case <-gathered:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	return t.pc.LocalDescription().SDP, nil
}

// AddICECandidate applies a candidate the peer trickled.
func (t *Transport) AddICECandidate(candidate webrtc.ICECandidateInit) error {
	return t.pc.AddICECandidate(candidate)
}

// AddRemoteVideoTrack adds a recvonly video transceiver, which the next offer
// negotiates.
func (t *Transport) AddRemoteVideoTrack() error {
	t.logger.Info("adding video transceiver")
	_, err := t.pc.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RTPTransceiverInit{
		Direction: webrtc.RTPTransceiverDirectionRecvonly,
	})
	return err
}

// RequestedVideoTracks returns how many video m-lines of the offer from
// SetOffer are recvonly and have no local track yet. Each local track added
// fills one of them.
func (t *Transport) RequestedVideoTracks() int {
	n := 0
	for _, tr := range t.pc.GetTransceivers() {
		if tr.Kind() == webrtc.RTPCodecTypeVideo && tr.Direction() == webrtc.RTPTransceiverDirectionSendonly && tr.Sender() == nil {
			n++
		}
	}
	return n
}

func (t *Transport) AddLocalTrack() (*RTPSender, error) {
	return t.addLocalTrack(webrtc.MimeTypeH264, "video")
}

func (t *Transport) AddLocalTrackWithCodec(codec string) (*RTPSender, error) {
	return t.addLocalTrack(codec, "video")
}

func (t *Transport) AddLocalTrackWithCodecAndID(codec string, id string) (*RTPSender, error) {
	return t.addLocalTrack(codec, id)
}

func (t *Transport) addLocalTrack(codec string, id string) (*RTPSender, error) {
	track, err := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{
		MimeType:     codec,
		ClockRate:    0,
		Channels:     0,
		SDPFmtpLine:  "",
		RTCPFeedback: []webrtc.RTCPFeedback{},
	}, id, "pion")
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
		rtcp:   newRTCPReceiver(sender, t.onCCFB),
	}, nil
}

// RTCPSender returns a sender for the RTCP a pipeline generates.
func (t *Transport) RTCPSender() *RTCPSender {
	return &RTCPSender{transport: t}
}

func (t *Transport) Close() error {
	return t.pc.Close()
}

func (t *Transport) onCCFB(report rtpfb.Report) error {
	t.logger.Debug("received ccfb packet report", "arrival", report.Arrival, "RTT", report.RTT)
	if t.rateController == nil {
		return nil
	}
	tr, err := t.rateController.onFeedback(report)
	if err != nil {
		return err
	}
	return t.applyTargetRate(tr)
}

// ControlBitrate makes the congestion controller steer the bitrate of source.
func (t *Transport) ControlBitrate(source mrtp.TargetBitrateSetter) {
	t.sourceLock.Lock()
	defer t.sourceLock.Unlock()
	t.source = source
}

// applyTargetRate sets the encoder's target rate and configures the pacer to
// run at pacingFactor times that rate, so the pacer's queue drains faster
// than media arrives and doesn't itself become a source of latency.
const pacingFactor = 1.5

// applyTargetRate ignores non-positive rates. SCReAM uses -1 to request a key
// frame, which is not forwarded to the encoder here.
func (t *Transport) applyTargetRate(tr float64) error {
	if tr <= 0 {
		return nil
	}
	t.sourceLock.Lock()
	source := t.source
	t.sourceLock.Unlock()
	if source != nil {
		if err := source.SetTargetBitrate(uint(tr)); err != nil {
			return err
		}
	}
	if t.pacer != nil {
		t.pacer.SetRate(t.pc.ID(), int(pacingFactor*tr))
	}
	return nil
}

type ecnLookupFunc func(ssrc uint32, sequenceNumber uint16) uint8

// GetECN implements rfc8888.ECNLookupTable.
func (f ecnLookupFunc) GetECN(ssrc uint32, sequenceNumber uint16) uint8 {
	return f(ssrc, sequenceNumber)
}

// getECN defers to the table SetNet installed, which may be applied after
// EnableCCFB.
func (t *Transport) getECN(ssrc uint32, sequenceNumber uint16) uint8 {
	if t.ecnTable == nil {
		return 0
	}
	return t.ecnTable.GetECN(ssrc, sequenceNumber)
}
