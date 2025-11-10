package mrtp

/*
#cgo pkg-config: vpx
#include "vpx/vpx_encoder.h"
#include "vpx/vp8cx.h"
#include "vpx/vpx_image.h"

vpx_codec_err_t vpx_codec_enc_init_macro(
	vpx_codec_ctx_t *ctx,
	vpx_codec_iface_t *iface,
	const vpx_codec_enc_cfg_t *cfg,
	vpx_codec_flags_t flags
) {
	return vpx_codec_enc_init(ctx, iface, cfg, flags);
}

void *pktBuf(vpx_codec_cx_pkt_t *pkt) {
  return pkt->data.frame.buf;
}

int pktSz(vpx_codec_cx_pkt_t *pkt) {
  return pkt->data.frame.sz;
}

vpx_codec_frame_flags_t pktFrameFlags(vpx_codec_cx_pkt_t *pkt) {
  return pkt->data.frame.flags;
}

*/
import "C"
import (
	"fmt"
	"image"
	"time"
	"unsafe"
)

func getEncoderByName(codec string) (*C.vpx_codec_iface_t, error) {
	switch codec {
	case "vp8":
		return C.vpx_codec_vp8_cx(), nil
	case "vp9":
		return C.vpx_codec_vp9_cx(), nil
	}
	return nil, fmt.Errorf("unknown codec: %v", codec)
}

type VP8Frame struct {
	IsKeyFrame bool
	Payload    []byte
}

type Encoder struct {
	encoder *C.vpx_codec_iface_t
	ctx     *C.vpx_codec_ctx_t
	cfg     *C.vpx_codec_enc_cfg_t

	start time.Time

	frame []byte
}

type Config struct {
	Codec       string
	Width       uint
	Heigth      uint
	TimebaseNum int
	TimebaseDen int
	TargetRate  uint
}

func NewEncoder(c Config) (*Encoder, error) {
	encoder, err := getEncoderByName(c.Codec)
	if err != nil {
		return nil, err
	}
	var cfg C.vpx_codec_enc_cfg_t
	if res := C.vpx_codec_enc_config_default(encoder, &cfg, 0); res != 0 {
		return nil, fmt.Errorf("failed to get encoder default config: %v", res)
	}

	cfg.g_w = C.uint(c.Width)
	cfg.g_h = C.uint(c.Heigth)
	cfg.g_timebase.num = 1    // C.int(c.TimebaseNum)
	cfg.g_timebase.den = 1000 // C.int(c.TimebaseDen)
	cfg.rc_end_usage = C.VPX_CBR
	cfg.rc_target_bitrate = C.uint(c.TargetRate)
	cfg.g_error_resilient = C.vpx_codec_er_flags_t(0)
	cfg.g_pass = C.VPX_RC_ONE_PASS
	cfg.g_threads = 20
	cfg.rc_resize_allowed = 0

	ctx := (*C.vpx_codec_ctx_t)(C.malloc(C.size_t(unsafe.Sizeof(C.vpx_codec_ctx_t{}))))
	if ctx == nil {
		return nil, fmt.Errorf("failed to allocate codec context")
	}
	res := C.vpx_codec_enc_init_macro(ctx, encoder, &cfg, 0)
	if res != 0 {
		return nil, fmt.Errorf("failed to init encoder")
	}
	return &Encoder{
		ctx:     ctx,
		encoder: encoder,
		cfg:     &cfg,
		start:   time.Time{},
		frame:   make([]byte, 0),
	}, nil
}

func (e *Encoder) Encode(
	image *image.YCbCr,
	ts time.Time,
	duration time.Duration,
) (*VP8Frame, error) {
	var pts int64
	if e.start.IsZero() {
		e.start = ts
	} else {
		pts = ts.Sub(e.start).Microseconds()
	}

	raw := C.vpx_img_alloc(
		nil,
		C.VPX_IMG_FMT_I420,
		C.uint(image.Bounds().Dx()),
		C.uint(image.Bounds().Dy()),
		1,
	)
	defer C.vpx_img_free(raw)

	raw.planes[0] = (*C.uchar)(unsafe.Pointer(&image.Y[0]))
	raw.planes[1] = (*C.uchar)(unsafe.Pointer(&image.Cb[0]))
	raw.planes[2] = (*C.uchar)(unsafe.Pointer(&image.Cr[0]))

	var flags int
	res := C.vpx_codec_encode(
		e.ctx,
		raw,
		C.vpx_codec_pts_t(pts),
		C.ulong(duration.Microseconds()),
		C.vpx_enc_frame_flags_t(flags),
		C.VPX_DL_REALTIME,
	)

	if res != C.VPX_CODEC_OK {
		return nil, fmt.Errorf("failed to encode frame: %v", res)
	}
	var iter C.vpx_codec_iter_t
	frame := &VP8Frame{}
	e.frame = e.frame[:0]
	for {
		pkt := C.vpx_codec_get_cx_data(e.ctx, &iter)
		if pkt == nil {
			break
		}
		if pkt.kind == C.VPX_CODEC_CX_FRAME_PKT {
			frame.IsKeyFrame = C.pktFrameFlags(pkt)&C.VPX_FRAME_IS_KEY == C.VPX_FRAME_IS_KEY
			encoded := C.GoBytes(unsafe.Pointer(C.pktBuf(pkt)), C.pktSz(pkt))
			e.frame = append(e.frame, encoded...)
		}
	}
	frame.Payload = make([]byte, len(e.frame))
	copy(frame.Payload, e.frame)
	return frame, nil
}
