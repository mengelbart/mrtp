package webrtc

import (
	"github.com/pion/webrtc/v4"
)

var ccfbFeedback = webrtc.RTCPFeedback{Type: webrtc.TypeRTCPFBACK, Parameter: "ccfb"}

// registerCCFB advertises CCFB for the codecs registered so far.
func (t *Transport) registerCCFB() {
	t.mediaEngine.RegisterFeedback(ccfbFeedback, webrtc.RTPCodecTypeVideo)
	t.mediaEngine.RegisterFeedback(ccfbFeedback, webrtc.RTPCodecTypeAudio)
}
