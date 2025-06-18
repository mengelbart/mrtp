package media

type Codec int

const (
	H264 Codec = iota
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
	}
	return "unknown"
}

func (c Codec) MediaType() string {
	switch c {
	case H264:
		return "video"
	}
	return "video"
}
