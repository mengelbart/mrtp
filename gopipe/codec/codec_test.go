//go:build cgo

package codec

import (
	"image"
	"testing"
	"time"

	"github.com/mengelbart/mrtp"
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
			encode, setRate, closeEncoder := newTestEncoder(t, Config{
				Codec:       c,
				Width:       testWidth,
				Height:      testHeight,
				TimebaseNum: testFPSNum,
				TimebaseDen: testFPSDen,
				TargetRate:  testBitrate,
			})
			defer closeEncoder()

			decode, closeDecoder := newTestDecoder(t, c)
			defer closeDecoder()

			frameDuration := time.Second * testFPSDen / testFPSNum

			for i := range testFrames {
				// exercise the path congestion control drives
				if i == testFrames/2 {
					setRate(testBitrate / 2)
				}

				frame, err := encode(
					testvideo.Image(testWidth, testHeight, i),
					int64(i)*frameDuration.Microseconds(),
					frameDuration,
				)
				require.NoError(t, err)
				require.NotEmpty(t, frame.Payload)

				// every encoded frame decodes to exactly one raw frame
				raw, err := decode(frame.Payload)
				require.NoError(t, err)

				assert.Equal(t, testWidth, raw.Width)
				assert.Equal(t, testHeight, raw.Height)
				assert.Equal(t, image.YCbCrSubsampleRatio420, raw.ChromaSubsampling)
				assert.Len(t, raw.Data, testWidth*testHeight*3/2)
			}
		})
	}
}

func newTestEncoder(t *testing.T, c Config) (
	encode func(*image.YCbCr, int64, time.Duration) (*Frame, error),
	setRate func(uint64),
	close func(),
) {
	t.Helper()

	switch c.Codec {
	case mrtp.VP8, mrtp.VP9:
		enc, err := NewVPXEncoder(c)
		require.NoError(t, err)
		return enc.Encode, enc.SetTargetRate, func() { assert.NoError(t, enc.Close()) }

	case mrtp.H264:
		enc, err := NewX264encoder(c)
		require.NoError(t, err)
		encode := func(img *image.YCbCr, _ int64, _ time.Duration) (*Frame, error) {
			return enc.Encode(img)
		}
		return encode, enc.SetTargetRate, func() { assert.NoError(t, enc.Close()) }
	}

	t.Fatalf("unsupported codec: %v", c.Codec)
	return nil, nil, nil
}

func newTestDecoder(t *testing.T, c mrtp.Codec) (decode func([]byte) (*DecodedFrame, error), close func()) {
	t.Helper()

	switch c {
	case mrtp.VP8, mrtp.VP9:
		dec, err := NewVPXDecoder(c)
		require.NoError(t, err)
		return dec.Decode, dec.Close

	case mrtp.H264:
		dec, err := NewH264Decoder()
		require.NoError(t, err)
		return dec.Decode, dec.Close
	}

	t.Fatalf("unsupported codec: %v", c)
	return nil, nil
}
