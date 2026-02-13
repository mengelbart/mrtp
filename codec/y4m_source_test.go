package codec

import (
	"log"
	"os"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mengelbart/y4m"
	"github.com/stretchr/testify/assert"
)

func TestY4MSource(t *testing.T) {
	// video file must exist
	if _, err := os.Stat("../simulation/Johnny_1280x720_60.y4m"); os.IsNotExist(err) {
		println("Video file not found. See simulation folder for more information.\n")
		t.Skip("video not found")
	}

	synctest.Test(t, func(t *testing.T) {

		file, err := os.Open("../simulation/Johnny_1280x720_60.y4m")
		assert.NoError(t, err)

		defer file.Close()
		_, h, err := y4m.NewReader(file)
		assert.NoError(t, err)

		_, err = file.Seek(0, 0)
		assert.NoError(t, err)

		encoder, err := NewEncoder(Config{
			Codec:       "vp8",
			Width:       uint(h.Width),
			Height:      uint(h.Height),
			TimebaseNum: h.FrameRate.Numerator,
			TimebaseDen: h.FrameRate.Denominator,
			TargetRate:  1_000_000,
		})
		assert.NoError(t, err)

		source, err := NewY4MSource(file, encoder)
		assert.NoError(t, err)

		log.SetFlags(log.Lmicroseconds)

		start := time.Now()
		ticker := time.NewTicker(30 * time.Millisecond)
		i := 0
		for range ticker.C {
			if i > 10 {
				break
			}
			i++
			_, err := source.GetFrame()
			assert.NoError(t, err)
		}
		log.Printf("encoded file in %v", time.Since(start))
	})
}
