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
	RTCPRecvFlowIDFlag FlagName = "rtcp-recv-flow-id"
	RTCPSendFlowIDFlag FlagName = "rtcp-send-flow-id"
)

// Flag vars
var (
	// Flow IDs for RoQ and datachannels
	RTCPRecvFlowID = uint(1)
	RTCPSendFlowID = uint(2)
)

type flagVar func(*flag.FlagSet)

func uintVar(p *uint, name FlagName, defaultValue *uint, usage string) func(*flag.FlagSet) {
	return func(fs *flag.FlagSet) {
		fs.UintVar(p, string(name), *defaultValue, usage)
	}
}

var flags = map[FlagName]flagVar{
	// flow ID flags
	RTCPRecvFlowIDFlag: uintVar(&RTCPRecvFlowID, RTCPRecvFlowIDFlag, &RTCPRecvFlowID, "RTP Flow ID when using RTP over QUIC"),
	RTCPSendFlowIDFlag: uintVar(&RTCPSendFlowID, RTCPSendFlowIDFlag, &RTCPSendFlowID, "RTP Flow ID when using RTP over QUIC"),
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
	RTCPRecvFlowID, RTCPSendFlowID = RTCPSendFlowID, RTCPRecvFlowID
}
