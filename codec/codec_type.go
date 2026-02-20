package codec

type CodecType int

const (
	VP8 CodecType = iota
	VP9
)

func (c CodecType) String() string {
	switch c {
	case VP8:
		return "vp8"
	case VP9:
		return "vp9"
	default:
		return "unknown"
	}
}
