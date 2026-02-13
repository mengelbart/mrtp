package codec

import (
	"io"
	"os"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mengelbart/y4m"
	"github.com/stretchr/testify/assert"
)

func TestVpxDecode(t *testing.T) {
	// video file must exist
	if _, err := os.Stat("../simulation/Johnny_1280x720_60.y4m"); os.IsNotExist(err) {
		println("Video file not found. See simulation folder for more information.\n")
		t.Skip("video not found")
	}

	synctest.Test(t, func(t *testing.T) {
		framesReceived := 0

		decoder, err := NewDecoder()
		assert.NoError(t, err)

		sink := WriterFunc(func(frame []byte, _ Attributes) error {
			img, err := decoder.Decode(frame)
			assert.NoError(t, err)
			assert.NotNil(t, img)

			framesReceived++

			return err
		})

		file, err := os.Open("../simulation/Johnny_1280x720_60.y4m")
		assert.NoError(t, err)

		defer file.Close()

		reader, streamHeader, err := y4m.NewReader(file)
		assert.NoError(t, err)

		i := Info{
			Width:       uint(streamHeader.Width),
			Height:      uint(streamHeader.Height),
			TimebaseNum: streamHeader.FrameRate.Numerator,
			TimebaseDen: streamHeader.FrameRate.Denominator,
		}
		encoder := NewVP8Encoder()

		writer, err := Chain(i, sink, encoder)
		assert.NoError(t, err)

		fps := float64(i.TimebaseNum) / float64(i.TimebaseDen)
		frameDuration := time.Duration(float64(time.Second) / fps)

		framesSent := 0

		ticker := time.NewTicker(frameDuration)
		defer ticker.Stop()
		for range ticker.C {
			frame, _, err := reader.ReadNextFrame()
			if err != nil {
				if err == io.EOF {
					println("sending done")
					break
				}
				assert.NoError(t, err)
				break
			}
			csr := convertSubsampleRatio(streamHeader.ChromaSubsampling)
			if err = writer.Write(frame, Attributes{
				ChromaSubsampling: csr,
			}); err != nil {
				assert.NoError(t, err)
				break
			}
			framesSent++
		}

		assert.Equal(t, framesSent, framesReceived)

		decoder.Close()
		synctest.Wait()
	})
}
