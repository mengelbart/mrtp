package codec

import (
	"context"
	"os"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestVpxDecode(t *testing.T) {
	// video file must exist
	if _, err := os.Stat("../simulation/Johnny_1280x720_60.y4m"); os.IsNotExist(err) {
		println("Video file not found. See simulation folder for more information.\n")
		t.Skip("video not found")
	}

	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())

		framesReceived := 0

		decoder, err := NewDecoder()
		assert.NoError(t, err)

		sink := WriterFunc(func(frame []byte, attr Attributes) error {
			rawFrame, attrs, err := decoder.Decode(frame, attr)
			assert.NoError(t, err)
			assert.NotNil(t, rawFrame)
			assert.NotNil(t, attrs)

			framesReceived++

			return err
		})

		file, err := os.Open("../simulation/Johnny_1280x720_60.y4m")
		assert.NoError(t, err)
		defer file.Close()

		fileSrc, err := NewY4MSource(file)
		assert.NoError(t, err)

		i := fileSrc.GetInfo()
		encoder := NewVP8Encoder()
		frameInter := newFrameInterceptor(false, 0, nil)

		writer, err := Chain(i, sink, encoder, frameInter)
		assert.NoError(t, err)

		fileSrc.StartLive(ctx, writer)

		assert.Equal(t, frameInter.count, framesReceived)

		decoder.Close()
		cancel()
		synctest.Wait()
	})
}

func TestVpxDecodeWithRTP(t *testing.T) {
	// video file must exist
	if _, err := os.Stat("../simulation/Johnny_1280x720_60.y4m"); os.IsNotExist(err) {
		println("Video file not found. See simulation folder for more information.\n")
		t.Skip("video not found")
	}

	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())

		framesReceived := 0

		decoder, err := NewDecoder()
		assert.NoError(t, err)

		timeout := 10 * time.Millisecond
		depacketizer := newRTPDepacketizer(timeout, func(frame []byte, pts int64) {
			rawFrame, attrs, err := decoder.Decode(frame, Attributes{PTS: pts})
			assert.NoError(t, err)
			assert.NotNil(t, rawFrame)
			assert.NotNil(t, attrs)
			framesReceived++
		})

		var wg sync.WaitGroup
		wg.Go(func() {
			depacketizer.Run()
		})

		sink := WriterFunc(func(b []byte, _ Attributes) error {
			return depacketizer.Write(b)
		})

		file, err := os.Open("../simulation/Johnny_1280x720_60.y4m")
		assert.NoError(t, err)
		defer file.Close()

		fileSrc, err := NewY4MSource(file)
		assert.NoError(t, err)

		i := fileSrc.GetInfo()
		encoder := NewVP8Encoder()
		packetizer := &RTPPacketizerFactory{
			MTU:       1420,
			PT:        96,
			SSRC:      0,
			ClockRate: 90_000,
		}
		pacer := &FrameSpacer{
			Ctx: ctx,
		}
		frameInter := newFrameInterceptor(false, 0, nil)

		writer, err := Chain(i, sink, pacer, packetizer, encoder, frameInter)
		assert.NoError(t, err)

		fileSrc.StartLive(ctx, writer)

		assert.Equal(t, frameInter.count, framesReceived)

		depacketizer.Close()
		cancel()
		synctest.Wait()
	})
}
