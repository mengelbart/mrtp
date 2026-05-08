package webrtc

// Everything below is copied from pion/webrtc/mux and unexported

func matchRange(lower, upper byte, buf []byte) bool {
	if len(buf) < 1 {
		return false
	}
	b := buf[0]

	return b >= lower && b <= upper
}

func matchSRTPOrSRTCP(b []byte) bool {
	return matchRange(128, 191, b)
}

func isRTCP(buf []byte) bool {
	// Not long enough to determine RTP/RTCP
	if len(buf) < 4 {
		return false
	}

	return buf[1] >= 192 && buf[1] <= 223
}

func matchSRTP(buf []byte) bool {
	return matchSRTPOrSRTCP(buf) && !isRTCP(buf)
}
