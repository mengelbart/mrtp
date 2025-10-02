package mrtp

type Codec int

const (
	H264 Codec = iota
	VP8
)

func (c Codec) ClockRate() int {
	switch c {
	default:
		return 90_000
	}
}

func (c Codec) String() string {
	switch c {
	case H264:
		return "H264"
	case VP8:
		return "VP8"
	}
	return "unknown"
}

func (c Codec) MediaType() string {
	switch c {
	case H264:
		return "video"
	case VP8:
		return "video"
	}
	return "video"
}
