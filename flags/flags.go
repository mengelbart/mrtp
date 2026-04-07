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
	RTPPortFlag      FlagName = "rtp-port"
	RTCPRecvPortFlag FlagName = "rtcp-recv-porto"
	RTCPSendPortFlag FlagName = "rtcp-send-porto"

	RTPFlowIDFlag          FlagName = "rtp-flow-id"
	RTCPRecvFlowIDFlag     FlagName = "rtcp-recv-flow-id"
	RTCPSendFlowIDFlag     FlagName = "rtcp-send-flow-id"
	DataChannelFlowIDFlag  FlagName = "dc-flow-id"
	NadaFeedbackFlowIDFlag FlagName = "nada-feedback-flow-id"
)

// Flag vars
var (
	// RTP Receive Port
	RTPPort      = uint(5000)
	RTCPSendPort = uint(5001)
	RTCPRecvPort = uint(5002)

	// Flow IDs for RoQ and datachannels
	RTPFlowID          = uint(0)
	RTCPRecvFlowID     = uint(1)
	RTCPSendFlowID     = uint(2)
	DataChannelFlowID  = uint(3)
	NadaFeedbackFlowID = uint(4)
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
	RTPPortFlag:      uintVar(&RTPPort, RTPPortFlag, &RTPPort, "UDP Port number for outgoing RTP stream"),
	RTCPRecvPortFlag: uintVar(&RTCPRecvPort, RTCPRecvPortFlag, &RTCPRecvPort, "UDP port for incoming RTCP stream"),
	RTCPSendPortFlag: uintVar(&RTCPSendPort, RTCPSendPortFlag, &RTCPSendPort, "UDP port for outgoing RTCP stream"),

	// flow ID flags
	RTPFlowIDFlag:          uintVar(&RTPFlowID, RTPFlowIDFlag, &RTPFlowID, "RTP Flow ID when using RTP over QUIC"),
	RTCPRecvFlowIDFlag:     uintVar(&RTCPRecvFlowID, RTCPRecvFlowIDFlag, &RTCPRecvFlowID, "RTP Flow ID when using RTP over QUIC"),
	RTCPSendFlowIDFlag:     uintVar(&RTCPSendFlowID, RTCPSendFlowIDFlag, &RTCPSendFlowID, "RTP Flow ID when using RTP over QUIC"),
	DataChannelFlowIDFlag:  uintVar(&DataChannelFlowID, DataChannelFlowIDFlag, &DataChannelFlowID, "Data Channel Flow ID when using quic data channels"),
	NadaFeedbackFlowIDFlag: uintVar(&NadaFeedbackFlowID, NadaFeedbackFlowIDFlag, &NadaFeedbackFlowID, "NADA Feedback Flow ID when using NADA or GCC with QUIC"),
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
	RTCPSendPort, RTCPRecvPort = RTCPRecvPort, RTCPSendPort
	RTCPRecvFlowID, RTCPSendFlowID = RTCPSendFlowID, RTCPRecvFlowID
}
