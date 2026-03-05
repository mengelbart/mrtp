package codec

import (
	"image"
	"io"
	"os"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/assert"
)

func TestX264Encoder(t *testing.T) {
	// video file must exist
	if _, err := os.Stat("../simulation/Johnny_1280x720_60.y4m"); os.IsNotExist(err) {
		println("Video file not found. See simulation folder for more information.\n")
		t.Skip("video not found")
	}

	synctest.Test(t, func(t *testing.T) {
		file, err := os.Open("../simulation/Johnny_1280x720_60.y4m")
		assert.NoError(t, err)
		defer file.Close()

		fileSrc, err := NewY4MSource(file)
		assert.NoError(t, err)
		i := fileSrc.GetInfo()

		conf := Config{
			Codec:      H264,
			Width:      i.Width,
			Height:     i.Height,
			TargetRate: 750_000,
		}

		enc, err := newX264encoder(conf)
		assert.NoError(t, err)

		for {
			frame, attr, err := fileSrc.getFrame()
			if err != nil {
				if err == io.EOF {
					break
				}
			}
			assert.NoError(t, err)

			// convert bytes to image.YCbCr
			// normaly done by Encoder
			csr, err := getChromaSubsampling(attr)
			assert.NoError(t, err)
			image := image.NewYCbCr(
				image.Rect(0, 0, int(i.Width), int(i.Height)),
				csr,
			)

			ySize := i.Width * i.Height
			uSize := ySize / 4
			image.Y = frame[:ySize]
			image.Cb = frame[ySize : ySize+uSize]
			image.Cr = frame[ySize+uSize:]

			encFrame, err := enc.encode(image)
			assert.NoError(t, err)
			assert.NotNil(t, encFrame)
			assert.Greater(t, len(encFrame.Payload), 0)
		}

		synctest.Wait()
	})
}
