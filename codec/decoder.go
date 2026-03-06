package codec

import "fmt"

func NewDecoder(codec CodecType) (Processor, error) {
	switch codec {
	case H264:
		return NewH264Decoder()
	case VP8, VP9:
		return NewVPXDecoder(codec)
	default:
		return nil, fmt.Errorf("unsupported codec: %v", codec)
	}
}
