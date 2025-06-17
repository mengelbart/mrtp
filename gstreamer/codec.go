package gstreamer

type Codec int

const (
	h264 Codec = iota
)

func (c Codec) ClockRate() int {
	switch c {
	default:
		return 90_000
	}
}

func (c Codec) String() string {
	switch c {
	case h264:
		return "H264"
	}
	return "unknown"
}

func (c Codec) MediaType() string {
	switch c {
	case h264:
		return "video"
	}
	return "video"
}
