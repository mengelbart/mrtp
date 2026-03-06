package codec

// #cgo pkg-config: x264
// #include "x264_bridge.h"
import "C"
import (
	"fmt"
	"image"
	"sync"
	"sync/atomic"
	"unsafe"
)

type x264encoder struct {
	engine *C.Encoder
	mu     sync.Mutex
	closed bool

	targetBitrate       atomic.Uint64 // kbps
	currentTrgetBitrate uint64        // kbps
}

func newX264encoder(c Config) (*x264encoder, error) {
	param := C.x264_param_t{
		i_csp:        C.X264_CSP_I420,
		i_width:      C.int(c.Width),
		i_height:     C.int(c.Height),
		i_fps_num:    C.uint(c.TimebaseNum),
		i_fps_den:    C.uint(c.TimebaseDen),
		i_keyint_max: C.int(60), // TODO: maybe based on fps?
	}
	param.rc.i_bitrate = C.int(c.TargetRate / 1000) // convert to kbps
	param.rc.i_vbv_max_bitrate = param.rc.i_bitrate
	param.rc.i_vbv_buffer_size = param.rc.i_vbv_max_bitrate // 1 second buffer for tighter latency

	var rc C.int
	// cPreset will be freed in C.enc_new
	cPreset := C.CString("ultrafast")
	engine := C.enc_new(param, cPreset, &rc)
	if rc != 0 {
		return nil, fmt.Errorf("failed to create x264 encoder with error code: %v", rc)
	}

	e := x264encoder{
		engine:              engine,
		currentTrgetBitrate: c.TargetRate,
	}
	return &e, nil
}

func (e *x264encoder) encode(image *image.YCbCr) (*Frame, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.closed {
		return nil, fmt.Errorf("encoder is closed")
	}

	bitrate := e.targetBitrate.Load()
	if bitrate != e.currentTrgetBitrate {
		errNum := C.apply_target_bitrate(e.engine, C.int(bitrate))
		if errNum != 0 {
			return nil, fmt.Errorf("failed to set x264encoder target rate with error code: %v", errNum)
		}
		e.currentTrgetBitrate = bitrate
	}

	var rc C.int
	s := C.enc_encode(
		e.engine,
		(*C.uchar)(&image.Y[0]),
		(*C.uchar)(&image.Cb[0]),
		(*C.uchar)(&image.Cr[0]),
		&rc,
	)
	if rc != 0 {
		return nil, fmt.Errorf("failed to encode image with error code: %v", rc)
	}

	encoded := C.GoBytes(unsafe.Pointer(s.data), s.data_len)

	frame := &Frame{
		Payload:    encoded,
		IsKeyFrame: false, // TODO
	}

	return frame, nil
}

func (e *x264encoder) setTargetRate(bitrate uint64) {
	e.targetBitrate.Store(bitrate)
}

func (e *x264encoder) close() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.closed {
		return nil
	}

	var rc C.int
	C.enc_close(e.engine, &rc)
	e.closed = true
	return nil
}
