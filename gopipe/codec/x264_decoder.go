package codec

/*
#cgo pkg-config: libavcodec libavutil
#include "x264_decoder_bridge.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"image"
	"unsafe"
)

var ErrFrameNotReady = errors.New("h264: frame not ready, decoder needs more input packets")

type H264Decoder struct {
	dec    *C.H264Decoder
	closed bool
}

func NewH264Decoder() (*H264Decoder, error) {
	var rc C.int
	dec := C.h264dec_new(&rc)
	if dec == nil {
		return nil, fmt.Errorf("h264dec_new failed: %d", int(rc))
	}
	return &H264Decoder{dec: dec}, nil
}

func (d *H264Decoder) Decode(encFrame []byte) (*DecodedFrame, error) {
	if d.closed {
		return nil, fmt.Errorf("decoder is closed")
	}

	cErr := C.h264dec_decode(d.dec, (*C.uint8_t)(unsafe.Pointer(&encFrame[0])), C.int(len(encFrame)))
	if cErr < 0 {
		return nil, fmt.Errorf("h264dec_decode failed: %d", int(cErr))
	}

	rc := C.h264dec_get_frame(d.dec)
	if rc == C.H264DEC_EAGAIN {
		return nil, ErrFrameNotReady
	}
	if rc < 0 {
		return nil, fmt.Errorf("h264dec_get_frame failed: %d", int(rc))
	}

	w := int(C.h264dec_width(d.dec))
	h := int(C.h264dec_height(d.dec))
	yStride := int(C.h264dec_y_linesize(d.dec))
	uStride := int(C.h264dec_u_linesize(d.dec))
	vStride := int(C.h264dec_v_linesize(d.dec))

	ySrc := unsafe.Slice((*byte)(unsafe.Pointer(C.h264dec_y_plane(d.dec))), yStride*h)
	uSrc := unsafe.Slice((*byte)(unsafe.Pointer(C.h264dec_u_plane(d.dec))), uStride*h/2)
	vSrc := unsafe.Slice((*byte)(unsafe.Pointer(C.h264dec_v_plane(d.dec))), vStride*h/2)

	// YUV 4:2:0
	ySize := w * h
	uSize := (w / 2) * (h / 2)
	frameData := make([]byte, ySize+uSize*2)

	// copy Y plane
	for r := range h {
		copy(frameData[r*w:r*w+w], ySrc[r*yStride:r*yStride+w])
	}

	// copy U plane
	uOffset := ySize
	for r := 0; r < h/2; r++ {
		copy(frameData[uOffset+r*(w/2):uOffset+r*(w/2)+(w/2)], uSrc[r*uStride:r*uStride+(w/2)])
	}

	// copy V plane
	vOffset := ySize + uSize
	for r := 0; r < h/2; r++ {
		copy(frameData[vOffset+r*(w/2):vOffset+r*(w/2)+(w/2)], vSrc[r*vStride:r*vStride+(w/2)])
	}

	return &DecodedFrame{
		Data:              frameData,
		Width:             w,
		Height:            h,
		ChromaSubsampling: image.YCbCrSubsampleRatio420,
	}, nil
}

func (d *H264Decoder) Close() {
	C.h264dec_free(d.dec)
	d.closed = true
}
