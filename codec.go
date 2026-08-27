package mrtp

import (
	"fmt"
	"strings"
)

// Codec identifies a media codec. It is the single codec type shared by the
// media pipelines (gstreamer, gopipe) and the transports.
type Codec int

const (
	H264 Codec = iota
	VP8
	VP9

	// Fake is a synthetic codec for RTP-only experiments. Its frames carry no
	// real media, only a size and a timestamp. Not every pipeline supports it.
	Fake
)

// NewCodec parses a codec name. Parsing is case insensitive.
func NewCodec(s string) (Codec, error) {
	switch strings.ToUpper(s) {
	case "H264":
		return H264, nil
	case "VP8":
		return VP8, nil
	case "VP9":
		return VP9, nil
	case "FAKE":
		return Fake, nil
	}
	return H264, fmt.Errorf("unknown codec: %s", s)
}

// String returns the canonical codec name, as used for RTP encoding names and
// as the subtype of the media MIME type.
func (c Codec) String() string {
	switch c {
	case H264:
		return "H264"
	case VP8:
		return "VP8"
	case VP9:
		return "VP9"
	case Fake:
		return "FAKE"
	}
	return "unknown"
}

// ClockRate returns the RTP clock rate in Hz.
func (c Codec) ClockRate() int {
	// All supported codecs are video codecs and use a 90 kHz clock.
	return 90_000
}

// MediaType returns the top level media type.
func (c Codec) MediaType() string {
	// All supported codecs are video codecs.
	return "video"
}

// MimeType returns the media type in "<media>/<codec>" form, as used by WebRTC
// and SDP.
func (c Codec) MimeType() string {
	return fmt.Sprintf("%v/%v", c.MediaType(), c)
}
