package gopipe

import (
	"context"
	"os"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mengelbart/mrtp/gopipe/codec"
	"github.com/stretchr/testify/assert"
)

func TestH264Decode(t *testing.T) {
	// video file must exist
	if _, err := os.Stat("../simulation/Johnny_1280x720_60.y4m"); os.IsNotExist(err) {
		println("Video file not found. See simulation folder for more information.\n")
		t.Skip("video not found")
	}

	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())

		framesReceived := 0

		decoder, err := codec.NewH264Decoder()
		assert.NoError(t, err)

		sink := WriterFunc(func(frame []byte, attr Attributes) error {
			rawFrame, err := decoder.Decode(frame)
			assert.NoError(t, err)
			assert.NotNil(t, rawFrame)

			framesReceived++

			return err
		})

		file, err := os.Open("../simulation/Johnny_1280x720_60.y4m")
		assert.NoError(t, err)
		defer file.Close()

		fileSrc, err := NewY4MSource(file)
		assert.NoError(t, err)

		i := fileSrc.GetInfo()
		encoder := NewEncoder(codec.H264)
		frameInter := newFrameInterceptor(false, 0, nil)

		pipeline, err := Chain(i, sink, encoder, frameInter)
		assert.NoError(t, err)

		fileSrc.StartLive(ctx, pipeline)

		assert.Equal(t, frameInter.count, framesReceived)

		decoder.Close()
		cancel()
		synctest.Wait()
	})
}

func TestH264DecodeWithRTP(t *testing.T) {
	// video file must exist
	if _, err := os.Stat("../simulation/Johnny_1280x720_60.y4m"); os.IsNotExist(err) {
		println("Video file not found. See simulation folder for more information.\n")
		t.Skip("video not found")
	}

	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())

		framesReceived := 0

		decoder, err := codec.NewH264Decoder()
		assert.NoError(t, err)

		timeout := 10 * time.Millisecond
		depacketizer, err := newRTPDepacketizer(timeout, codec.H264, func(frame []byte, pts int64) {
			rawFrame, err := decoder.Decode(frame)
			assert.NoError(t, err)
			assert.NotNil(t, rawFrame)
			framesReceived++
		})
		assert.NoError(t, err)

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
		encoder := NewEncoder(codec.H264)
		packetizer := &RTPPacketizerFactory{
			MTU:       1420,
			PT:        96,
			SSRC:      0,
			ClockRate: 90_000,
			Codec:     codec.H264,
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
