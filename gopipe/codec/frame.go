package codec

import "image"

type DecodedFrame struct {
	Data              []byte
	Width             int
	Height            int
	ChromaSubsampling image.YCbCrSubsampleRatio
}
