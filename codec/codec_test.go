//go:build cgo

package codec_test

import (
	"image"
	"testing"
	"time"

	"github.com/mengelbart/mrtp"
	"github.com/mengelbart/mrtp/codec"
	"github.com/mengelbart/mrtp/codec/avcodec"
	"github.com/mengelbart/mrtp/codec/vpx"
	"github.com/mengelbart/mrtp/codec/x264"
	"github.com/mengelbart/mrtp/internal/testvideo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testWidth   = 320
	testHeight  = 240
	testFrames  = 10
	testFPSNum  = 30
	testFPSDen  = 1
	testBitrate = 750_000
)

func TestEncodeDecodeRoundtrip(t *testing.T) {
	for _, c := range []mrtp.Codec{mrtp.VP8, mrtp.VP9, mrtp.H264} {
		t.Run(c.String(), func(t *testing.T) {
			enc := newTestEncoder(t, codec.Config{
				Codec:      c,
				Width:      testWidth,
				Height:     testHeight,
				FrameRate:  mrtp.FrameRate{Num: testFPSNum, Den: testFPSDen},
				TargetRate: testBitrate,
			})
			defer func() { assert.NoError(t, enc.Close()) }()

			dec := newTestDecoder(t, c)
			defer func() { assert.NoError(t, dec.Close()) }()

			frameDuration := time.Second * testFPSDen / testFPSNum

			for i := range testFrames {
				// exercise the path congestion control drives
				if i == testFrames/2 {
					enc.SetTargetRate(testBitrate / 2)
				}

				frame, err := enc.Encode(
					testvideo.Image(testWidth, testHeight, i),
					int64(i)*frameDuration.Microseconds(),
					frameDuration,
				)
				require.NoError(t, err)
				require.NotEmpty(t, frame.Payload)

				// every encoded frame decodes to exactly one raw frame
				raw, err := dec.Decode(frame.Payload)
				require.NoError(t, err)

				assert.Equal(t, testWidth, raw.Width)
				assert.Equal(t, testHeight, raw.Height)
				assert.Equal(t, image.YCbCrSubsampleRatio420, raw.ChromaSubsampling)
				assert.Len(t, raw.Data, testWidth*testHeight*3/2)
			}
		})
	}
}

func newTestEncoder(t *testing.T, c codec.Config) codec.Encoder {
	t.Helper()

	switch c.Codec {
	case mrtp.VP8, mrtp.VP9:
		enc, err := vpx.NewEncoder(c)
		require.NoError(t, err)
		return enc

	case mrtp.H264:
		enc, err := x264.NewEncoder(c)
		require.NoError(t, err)
		return enc
	}

	t.Fatalf("unsupported codec: %v", c.Codec)
	return nil
}

func newTestDecoder(t *testing.T, c mrtp.Codec) codec.Decoder {
	t.Helper()

	switch c {
	case mrtp.VP8, mrtp.VP9:
		dec, err := vpx.NewDecoder(c)
		require.NoError(t, err)
		return dec

	case mrtp.H264:
		dec, err := avcodec.NewH264Decoder()
		require.NoError(t, err)
		return dec
	}

	t.Fatalf("unsupported codec: %v", c)
	return nil
}
