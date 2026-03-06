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
	"log/slog"
	"maps"
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

func (d *H264Decoder) Decode(encFrame []byte, attrs Attributes) ([]byte, Attributes, error) {
	if d.closed {
		return nil, nil, fmt.Errorf("decoder is closed")
	}

	cErr := C.h264dec_decode(d.dec, (*C.uint8_t)(unsafe.Pointer(&encFrame[0])), C.int(len(encFrame)))
	if cErr < 0 {
		return nil, nil, fmt.Errorf("h264dec_decode failed: %d", int(cErr))
	}

	rc := C.h264dec_get_frame(d.dec)
	if rc == C.H264DEC_EAGAIN {
		return nil, nil, ErrFrameNotReady
	}
	if rc < 0 {
		return nil, nil, fmt.Errorf("h264dec_get_frame failed: %d", int(rc))
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

	pts, err := getPTS(attrs)
	if err != nil {
		return nil, nil, err
	}
	slog.Info("decoder src", "length", len(frameData), "pts", pts)

	attrs[Width] = w
	attrs[Height] = h
	attrs[ChromaSubsampling] = image.YCbCrSubsampleRatio420

	return frameData, attrs, nil
}

func (d *H264Decoder) Close() {
	C.h264dec_free(d.dec)
	d.closed = true
}

func (d *H264Decoder) Link(next Writer, i Info) (Writer, error) {
	return WriterFunc(func(encFrame []byte, attrs Attributes) error {
		rawFrame, frameAttrs, err := d.Decode(encFrame, attrs)
		if err != nil {
			if errors.Is(err, ErrFrameNotReady) {
				slog.Info("decoder: frame not ready, need more data")
				// Frame not ready, end pipeline chain here
				return nil
			}
			return err
		}

		// merge attributes
		if attrs == nil {
			attrs = make(Attributes)
		}
		maps.Copy(attrs, frameAttrs)

		return next.Write(rawFrame, attrs)
	}), nil
}
