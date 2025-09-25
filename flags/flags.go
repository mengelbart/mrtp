// Package flags implements command-line flags for mrtp.
//
// The design idea is taken from [upspin.io/flags], but most of the code is
// modified. This package uses a slightly modified version of [RegisterInto] and
// the internal [flags]-map. See [Upspin LICENSE] for upspins copyright and
// license information.
//
// [upspin.io/flags]: https://github.com/upspin/upspin/tree/334f107fe3d98225d7adfbb35b74e066fbca9875/flags
// [Upspin LICENSE]: https://github.com/upspin/upspin/blob/334f107fe3d98225d7adfbb35b74e066fbca9875/LICENSE
package flags

import (
	"flag"
	"fmt"
)

type FlagName string

// flag keys
const (
	LocalAddrFlag  FlagName = "local"
	RemoteAddrFlag FlagName = "remote"
	HTTPAddrFlag   FlagName = "http-address"
	HTTPSAddrFlag  FlagName = "https-address"

	RTPPortFlag      FlagName = "rtp-port"
	RTCPRecvPortFlag FlagName = "rtcp-recv-porto"
	RTCPSendPortFlag FlagName = "rtcp-send-porto"

	RTPFlowIDFlag          FlagName = "rtp-flow-id"
	RTCPRecvFlowIDFlag     FlagName = "rtcp-recv-flow-id"
	RTCPSendFlowIDFlag     FlagName = "rtcp-send-flow-id"
	DataChannelFlowIDFlag  FlagName = "dc-flow-id"
	NadaFeedbackFlowIDFlag FlagName = "nada-feedback-flow-id"

	CertFlag FlagName = "cert"
	KeyFlag  FlagName = "key"

	RoQServerFlag FlagName = "roq-server"
	RoQClientFlag FlagName = "roq-client"

	GstCCFBFlag FlagName = "gst-ccfb"

	SinkTypeFlag FlagName = "sink-type"
	LocationFlag FlagName = "location"

	TraceRTPRecvFlag FlagName = "trace-rtp-recv"
	TraceRTPSendFlag FlagName = "trace-rtp-send"

	CCnadaFlag        FlagName = "nada"
	CCgccFlag         FlagName = "pion-gcc"
	MaxTragetRateFlag FlagName = "max-target-rate"
	NadaFeedbackFlag  FlagName = "nada-feedback"

	QuicCCFlag  FlagName = "quic-cc"
	LogQuicFlag FlagName = "log-quic"

	DataChannelFlag FlagName = "dc"
)

// default values
const (
	defaultAddr      = "127.0.0.1"
	defaultHTTPAddr  = "127.0.0.1:8080"
	defaultHTTPSAddr = "127.0.0.1:4443"

	defaultCert = "localhost.pem"
	defaultKey  = "localhost-key.pem"

	defaultRTPPort      = uint(5000)
	defaultRTCPSendPort = uint(5001)
	defaultRTCPRecvPort = uint(5002)

	defaultRTPFlowID          = uint(0)
	defaultRTCPRecvFlowID     = uint(1)
	defaultRTCPSendFlowID     = uint(2)
	defaultDataChannelFlowID  = uint(3)
	defaultNadaFeedbackFlowID = uint(4)

	defaultLocation = "videotestsrc"

	defaultSinkType      = uint(0)         // Corresponds to autovideosink
	defaultMaxTargetRate = uint(3_000_000) // 3 Mbps

	defaultQuicCC = uint(0)
)

// Flag vars
var (
	// LocalAddr
	LocalAddr = defaultAddr

	// RemoteAddr
	RemoteAddr = defaultAddr

	// HTTP Server
	HTTPAddr = defaultHTTPAddr

	HTTPSAddr = defaultHTTPSAddr

	Cert = defaultCert

	Key = defaultKey

	// RTP Receive Port
	RTPPort = defaultRTPPort

	RTCPRecvPort = defaultRTCPRecvPort

	RTCPSendPort = defaultRTCPSendPort

	// Flow IDs for RoQ and datachannels
	RTPFlowID = defaultRTPFlowID

	RTCPRecvFlowID = defaultRTCPRecvFlowID

	RTCPSendFlowID = defaultRTCPSendFlowID

	DataChannelFlowID = defaultDataChannelFlowID

	NadaFeedbackFlowID = defaultNadaFeedbackFlowID

	RoQServer = false

	RoQClient = false

	DataChannel = false

	GstCCFB = false

	SinkType = defaultSinkType

	Location = defaultLocation

	TraceRTPRecv = false

	TraceRTPSend = false

	CCnada = false

	CCgcc = false

	NadaFeedback = false

	// MaxTargetRate is the max target rate in bits per second
	MaxTargetRate = defaultMaxTargetRate

	QuicCC = defaultQuicCC

	LogQuic = false
)

type flagVar func(*flag.FlagSet)

func stringVar(p *string, name FlagName, defaultValue *string, usage string) func(*flag.FlagSet) {
	return func(fs *flag.FlagSet) {
		fs.StringVar(p, string(name), *defaultValue, usage)
	}
}

func uintVar(p *uint, name FlagName, defaultValue *uint, usage string) func(*flag.FlagSet) {
	return func(fs *flag.FlagSet) {
		fs.UintVar(p, string(name), *defaultValue, usage)
	}
}

func boolVar(p *bool, name FlagName, defaultValue *bool, usage string) func(*flag.FlagSet) {
	return func(fs *flag.FlagSet) {
		fs.BoolVar(p, string(name), *defaultValue, usage)
	}
}

var flags = map[FlagName]flagVar{
	// Address related flags
	LocalAddrFlag:  stringVar(&LocalAddr, LocalAddrFlag, &LocalAddr, "Address for local servers"),
	RemoteAddrFlag: stringVar(&RemoteAddr, RemoteAddrFlag, &RemoteAddr, "Address for remote servers"),
	HTTPAddrFlag:   stringVar(&HTTPAddr, HTTPAddrFlag, &HTTPAddr, "HTTP Server address"),
	HTTPSAddrFlag:  stringVar(&HTTPSAddr, HTTPSAddrFlag, &HTTPSAddr, "HTTPS Server address"),

	RTPPortFlag:      uintVar(&RTPPort, RTPPortFlag, &RTPPort, "UDP Port number for outgoing RTP stream"),
	RTCPRecvPortFlag: uintVar(&RTCPRecvPort, RTCPRecvPortFlag, &RTCPRecvPort, "UDP port for incoming RTCP stream"),
	RTCPSendPortFlag: uintVar(&RTCPSendPort, RTCPSendPortFlag, &RTCPSendPort, "UDP port for outgoing RTCP stream"),

	// flow ID flags
	RTPFlowIDFlag:          uintVar(&RTPFlowID, RTPFlowIDFlag, &RTPFlowID, "RTP Flow ID when using RTP over QUIC"),
	RTCPRecvFlowIDFlag:     uintVar(&RTCPRecvFlowID, RTCPRecvFlowIDFlag, &RTCPRecvFlowID, "RTP Flow ID when using RTP over QUIC"),
	RTCPSendFlowIDFlag:     uintVar(&RTCPSendFlowID, RTCPSendFlowIDFlag, &RTCPSendFlowID, "RTP Flow ID when using RTP over QUIC"),
	DataChannelFlowIDFlag:  uintVar(&DataChannelFlowID, DataChannelFlowIDFlag, &DataChannelFlowID, "Data Channel Flow ID when using quic data channels"),
	NadaFeedbackFlowIDFlag: uintVar(&NadaFeedbackFlowID, NadaFeedbackFlowIDFlag, &NadaFeedbackFlowID, "NADA Feedback Flow ID when using NADA or GCC with QUIC"),

	// TLS Certificate
	CertFlag: stringVar(&Cert, CertFlag, &Cert, "TLS Certificate"),
	KeyFlag:  stringVar(&Key, KeyFlag, &Key, "TLS Certificate key"),

	// RoQ Flags
	RoQServerFlag:   boolVar(&RoQServer, RoQServerFlag, &RoQServer, "Use RoQ server transport."),
	RoQClientFlag:   boolVar(&RoQClient, RoQClientFlag, &RoQClient, "Use RoQ client transport."),
	DataChannelFlag: boolVar(&DataChannel, DataChannelFlag, &DataChannel, "Send/Receive data with data channels"),

	// CC flags
	GstCCFBFlag: boolVar(&GstCCFB, GstCCFBFlag, &GstCCFB, "Send CCFB RTCP Feedback packets generated by the screamrx Gstreamer element"),

	// IO Flags
	SinkTypeFlag: uintVar(&SinkType, SinkTypeFlag, &SinkType, "Sink type (0: autovideosink, 1: filesink, requires <location> to be set, 2: fakesink)"),
	LocationFlag: stringVar(&Location, LocationFlag, &Location, "Location for filesrc or filesink (if <sink-type> is 1 (filesink))"),

	// tracing flags
	TraceRTPRecvFlag: boolVar(&TraceRTPRecv, TraceRTPRecvFlag, &TraceRTPRecv, "Log incoming RTP packets"),
	TraceRTPSendFlag: boolVar(&TraceRTPSend, TraceRTPSendFlag, &TraceRTPSend, "Log outgoing RTP packets"),

	// CC flags
	CCnadaFlag:        boolVar(&CCnada, CCnadaFlag, &CCnada, "Enable NADA congestion control"),
	CCgccFlag:         boolVar(&CCgcc, CCgccFlag, &CCgcc, "Enable GCC congestion control"),
	MaxTragetRateFlag: uintVar(&MaxTargetRate, MaxTragetRateFlag, &MaxTargetRate, "Set the maximum target rate in bits per second of the congestion controler"),
	NadaFeedbackFlag:  boolVar(&NadaFeedback, NadaFeedbackFlag, &NadaFeedback, "Send NADA feedback"),

	// QUIC flags
	QuicCCFlag:  uintVar(&QuicCC, QuicCCFlag, &QuicCC, "Which quic CC to use. 0: Reno, 1: no CC and no pacer, 2: only pacer"),
	LogQuicFlag: boolVar(&LogQuic, LogQuicFlag, &LogQuic, "Log quic internal events"),
}

func RegisterInto(fs *flag.FlagSet, names ...FlagName) {
	if len(names) == 0 {
		for _, f := range flags {
			f(fs)
		}
	} else {
		for _, n := range names {
			f, ok := flags[n]
			if !ok {
				panic(fmt.Sprintf("unknown flag: %q", n))
			}
			f(fs)
		}
	}
}

// SwapRTCPDefaults swaps the default values for RTCP ports and flow IDs.
// This needs to be done for one side, as these ports and flow IDs are asymmetric.
func SwapRTCPDefaults() {
	RTCPSendPort = defaultRTCPRecvPort
	RTCPRecvPort = defaultRTCPSendPort
	RTCPRecvFlowID = defaultRTCPSendFlowID
	RTCPSendFlowID = defaultRTCPRecvFlowID
}
