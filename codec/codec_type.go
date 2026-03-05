package codec

import (
	"fmt"
	"strings"
)

type CodecType int

const (
	VP8 CodecType = iota
	VP9
	H264
)

func CodecTypeFromString(s string) (CodecType, error) {
	switch strings.ToLower(s) {
	case "vp8":
		return VP8, nil
	case "vp9":
		return VP9, nil
	case "h264":
		return H264, nil
	}
	return VP8, fmt.Errorf("unknown codec: %s", s)
}

func (c CodecType) String() string {
	switch c {
	case VP8:
		return "vp8"
	case VP9:
		return "vp9"
	case H264:
		return "h264"
	default:
		return "unknown"
	}
}
