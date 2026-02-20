package codec

import (
	"fmt"
	"image"
	"log/slog"
	"maps"
	"unsafe"
)

/*
#cgo pkg-config: vpx
#include <stdlib.h>
#include <vpx/vpx_decoder.h>
#include <vpx/vp8dx.h>
#include <vpx/vpx_image.h>


vpx_codec_iface_t *ifaceVP8Decoder() {
   return vpx_codec_vp8_dx();
}
vpx_codec_iface_t *ifaceVP9Decoder() {
   return vpx_codec_vp9_dx();
}

// Allocates a new decoder context
vpx_codec_ctx_t* newDecoderCtx() {
    return (vpx_codec_ctx_t*)malloc(sizeof(vpx_codec_ctx_t));
}

// Initializes the decoder
vpx_codec_err_t decoderInit(vpx_codec_ctx_t* ctx, vpx_codec_iface_t* iface) {
    return vpx_codec_dec_init_ver(ctx, iface, NULL, 0, VPX_DECODER_ABI_VERSION);
}

// Decodes an encoded frame
vpx_codec_err_t decodeFrame(vpx_codec_ctx_t* ctx, const uint8_t* data, unsigned int data_sz) {
    return vpx_codec_decode(ctx, data, data_sz, NULL, 0);
}

// Creates an iterator
vpx_codec_iter_t* newIter() {
    return (vpx_codec_iter_t*)malloc(sizeof(vpx_codec_iter_t));
}

// Returns the next decoded frame
vpx_image_t* getFrame(vpx_codec_ctx_t* ctx, vpx_codec_iter_t* iter) {
    return vpx_codec_get_frame(ctx, iter);
}

// Frees a decoded frane
void freeFrame(vpx_image_t* f) {
	vpx_img_free(f);
}

// Frees a decoder context
void freeDecoderCtx(vpx_codec_ctx_t* ctx) {
    vpx_codec_destroy(ctx);
    free(ctx);
}

*/
import "C"

type Decoder struct {
	codecCtx *C.vpx_codec_ctx_t
	closed   bool

	iter C.vpx_codec_iter_t
}

func NewDecoder() (*Decoder, error) {
	codec := C.newDecoderCtx()
	if C.decoderInit(codec, C.ifaceVP8Decoder()) != C.VPX_CODEC_OK {
		return nil, fmt.Errorf("vpx_codec_dec_init failed")
	}

	return &Decoder{
		codecCtx: codec,
	}, nil
}

func (d *Decoder) Decode(encFrame []byte, attrs Attributes) ([]byte, Attributes, error) {
	if d.closed {
		return nil, nil, fmt.Errorf("decoder is closed")
	}

	status := C.decodeFrame(d.codecCtx, (*C.uint8_t)(&encFrame[0]), C.uint(len(encFrame)))
	if status != C.VPX_CODEC_OK {
		return nil, nil, fmt.Errorf("decode failed: %v", status)
	}

	d.iter = nil

	input := C.getFrame(d.codecCtx, &d.iter)
	if input == nil {
		return nil, nil, fmt.Errorf("decode failed: no image in decoder")
	}

	w := int(input.d_w)
	h := int(input.d_h)
	yStride := int(input.stride[0])
	uStride := int(input.stride[1])
	vStride := int(input.stride[2])

	ySrc := unsafe.Slice((*byte)(unsafe.Pointer(input.planes[0])), yStride*h)
	uSrc := unsafe.Slice((*byte)(unsafe.Pointer(input.planes[1])), uStride*h/2)
	vSrc := unsafe.Slice((*byte)(unsafe.Pointer(input.planes[2])), vStride*h/2)

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

	C.freeFrame(input)

	// log frame info
	pts, err := getPTS(attrs)
	if err != nil {
		return nil, nil, err
	}
	slog.Info("decoder src", "length", len(frameData), "pts", pts)

	// add metadata to attributes
	attrs[Width] = w
	attrs[Height] = h
	attrs[ChromaSubsampling] = image.YCbCrSubsampleRatio420

	return frameData, attrs, nil
}

func (d *Decoder) Close() {
	C.freeDecoderCtx(d.codecCtx)
	d.closed = true
}

func (d *Decoder) Link(next Writer, i Info) (Writer, error) {
	return WriterFunc(func(encFrame []byte, attrs Attributes) error {
		rawFrame, frameAttrs, err := d.Decode(encFrame, attrs)
		if err != nil {
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
