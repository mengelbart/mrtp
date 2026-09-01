// Package testvideo generates synthetic I420 video for tests, so they need no video file.
package testvideo

import (
	"bytes"
	"fmt"
	"image"
	"math/rand/v2"
)

// frame returns a single I420 frame whose content shifts with frameIdx. The noise keeps
// encoded frames from compressing down to a single RTP packet.
func frame(width, height, frameIdx int) []byte {
	ySize := width * height
	uSize := ySize / 4
	buf := make([]byte, ySize+2*uSize)

	rng := rand.New(rand.NewPCG(uint64(frameIdx), 1))
	for y := range height {
		for x := range width {
			buf[y*width+x] = byte(x + y + frameIdx*4 + int(rng.Uint32()&0x7f))
		}
	}
	cb := buf[ySize : ySize+uSize]
	cr := buf[ySize+uSize:]
	for i := range cb {
		cb[i] = byte(64 + frameIdx)
		cr[i] = byte(192 - frameIdx)
	}
	return buf
}

// Image returns a synthetic frame as the image the encoders take.
func Image(width, height, frameIdx int) *image.YCbCr {
	buf := frame(width, height, frameIdx)
	ySize := width * height
	uSize := ySize / 4

	return &image.YCbCr{
		Y:              buf[:ySize],
		Cb:             buf[ySize : ySize+uSize],
		Cr:             buf[ySize+uSize:],
		YStride:        width,
		CStride:        width / 2,
		SubsampleRatio: image.YCbCrSubsampleRatio420,
		Rect:           image.Rect(0, 0, width, height),
	}
}

// Y4M returns an in-memory y4m stream of the given number of synthetic frames.
func Y4M(width, height, frames, fpsNum, fpsDen int) []byte {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "YUV4MPEG2 W%d H%d F%d:%d Ip A0:0 C420jpeg\n", width, height, fpsNum, fpsDen)
	for i := range frames {
		buf.WriteString("FRAME\n")
		buf.Write(frame(width, height, i))
	}
	return buf.Bytes()
}
