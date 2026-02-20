package codec

import (
	"context"
	"log/slog"
	"os"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
	"github.com/stretchr/testify/assert"
)

func TestDepacketizer(t *testing.T) {
	// video file must exist
	if _, err := os.Stat("../simulation/Johnny_1280x720_60.y4m"); os.IsNotExist(err) {
		println("Video file not found. See simulation folder for more information.\n")
		t.Skip("video not found")
	}

	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())

		framesReceived := 0

		timeout := 10 * time.Millisecond
		depacketizer := newRTPDepacketizer(timeout, func(frame []byte, pts int64) {
			slog.Info("got frame", "size", len(frame))
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
		rtpPipeline, err := Chain(i, sink, pacer, packetizer, encoder, frameInter)
		assert.NoError(t, err)

		fileSrc.StartLive(ctx, rtpPipeline)

		assert.Equal(t, frameInter.count, framesReceived)

		depacketizer.Close()
		cancel()
		synctest.Wait()
	})
}

func TestDepacketizerFrameIntegrity(t *testing.T) {
	// video file must exist
	if _, err := os.Stat("../simulation/Johnny_1280x720_60.y4m"); os.IsNotExist(err) {
		println("Video file not found. See simulation folder for more information.\n")
		t.Skip("video not found")
	}

	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		const maxFrames = 30

		receivedFrames := make([][]byte, 0, maxFrames)

		timeout := 10 * time.Millisecond
		receivedFrameCount := 0
		depacketizer := newRTPDepacketizer(timeout, func(frame []byte, pts int64) {
			if receivedFrameCount < maxFrames {
				frameCopy := make([]byte, len(frame))
				copy(frameCopy, frame)
				receivedFrames = append(receivedFrames, frameCopy)
			}
			receivedFrameCount++
		})

		var wg sync.WaitGroup
		wg.Go(func() {
			depacketizer.Run()
		})

		// sink writes to depacketizer
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

		frameInter := newFrameInterceptor(true, maxFrames, nil)

		rtpPipeline, err := Chain(i, sink, pacer, packetizer, frameInter, encoder)
		assert.NoError(t, err)

		fps := float64(i.TimebaseNum) / float64(i.TimebaseDen)
		frameDuration := time.Duration(float64(time.Second) / fps)

		ticker := time.NewTicker(frameDuration)
		defer ticker.Stop()

		fileSrc.StartLive(ctx, rtpPipeline)

		time.Sleep(100 * time.Millisecond)

		// verify frame counts match
		assert.Equal(t, maxFrames, len(frameInter.sentFrames), "interceptor should have captured %d frames", maxFrames)
		assert.Equal(t, maxFrames, len(receivedFrames), "should have received %d frames", maxFrames)
		assert.Equal(t, frameInter.count, receivedFrameCount, "total sent and received frame counts should match")

		// compare each frame
		for i := 0; i < len(frameInter.sentFrames); i++ {
			assert.Equal(t, len(frameInter.sentFrames[i]), len(receivedFrames[i]),
				"frame %d: length mismatch", i)
			assert.Equal(t, frameInter.sentFrames[i], receivedFrames[i],
				"frame %d: content mismatch", i)
			slog.Info("frame comparison", "index", i, "size", len(frameInter.sentFrames[i]), "match", true)
		}

		depacketizer.Close()
		wg.Wait()
		synctest.Wait()
	})
}

func TestDepacketizerRTPdrops(t *testing.T) {
	// video file must exist
	if _, err := os.Stat("../simulation/Johnny_1280x720_60.y4m"); os.IsNotExist(err) {
		println("Video file not found. See simulation folder for more information.\n")
		t.Skip("video not found")
	}

	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		const maxFrames = 30
		framesToBeDropped := []int{3, 24, 25}

		maxReceiveFrames := maxFrames - len(framesToBeDropped)
		receivedFrames := make([][]byte, 0, maxReceiveFrames)

		timeout := 10 * time.Millisecond
		receivedFrameCount := 0
		depacketizer := newRTPDepacketizer(timeout, func(frame []byte, pts int64) {
			if receivedFrameCount < maxReceiveFrames {
				frameCopy := make([]byte, len(frame))
				copy(frameCopy, frame)
				receivedFrames = append(receivedFrames, frameCopy)
			}
			receivedFrameCount++
		})

		var wg sync.WaitGroup
		wg.Go(func() {
			depacketizer.Run()
		})

		// Sink writes to depacketizer
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

		frameInter := newFrameInterceptor(true, maxFrames, framesToBeDropped)
		dropInter := newRtpDropInterceptor()

		rtpPipeline, err := Chain(i, sink, pacer, dropInter, packetizer, frameInter, encoder)
		assert.NoError(t, err)

		fps := float64(i.TimebaseNum) / float64(i.TimebaseDen)
		frameDuration := time.Duration(float64(time.Second) / fps)

		ticker := time.NewTicker(frameDuration)
		defer ticker.Stop()

		fileSrc.StartLive(ctx, rtpPipeline)

		time.Sleep(100 * time.Millisecond)

		// verify frame counts match
		assert.Equal(t, maxFrames, len(frameInter.sentFrames), "interceptor should have captured %d frames", maxFrames)
		assert.Equal(t, maxReceiveFrames, len(receivedFrames), "received frame saver should have saved %d frames", maxReceiveFrames)

		expectedReceivedFrames := frameInter.count - len(framesToBeDropped)
		assert.Equal(t, expectedReceivedFrames, receivedFrameCount)

		// compare each frame, skipping the dropped ones
		receivedIdx := 0
		for sentIdx := 0; sentIdx < len(frameInter.sentFrames); sentIdx++ {
			frameNum := sentIdx
			if slices.Contains(framesToBeDropped, frameNum) {
				continue
			}

			assert.Less(t, receivedIdx, len(receivedFrames), "received index %d out of range", receivedIdx)
			assert.Equal(t, len(frameInter.sentFrames[sentIdx]), len(receivedFrames[receivedIdx]))
			assert.Equal(t, frameInter.sentFrames[sentIdx], receivedFrames[receivedIdx])
			receivedIdx++
		}

		depacketizer.Close()
		wg.Wait()
		synctest.Wait()
	})
}

type frameInterceptor struct {
	saveFrame        bool
	sentFrames       [][]byte
	maxSave          int
	framesToBeMarked []int

	count int
}

func newFrameInterceptor(saveFrame bool, maxSave int, framesToBeMarked []int) *frameInterceptor {
	return &frameInterceptor{
		saveFrame:        saveFrame,
		sentFrames:       make([][]byte, 0),
		maxSave:          maxSave,
		framesToBeMarked: framesToBeMarked,
	}
}

func (i *frameInterceptor) Link(w Writer, _ Info) (Writer, error) {
	return WriterFunc(func(b []byte, a Attributes) error {
		if i.saveFrame && len(i.sentFrames) < i.maxSave {
			frameCopy := make([]byte, len(b))
			copy(frameCopy, b)
			i.sentFrames = append(i.sentFrames, frameCopy)
		}

		if slices.Contains(i.framesToBeMarked, i.count) {
			a["DROP"] = true
		}
		i.count++

		return w.Write(b, a)
	}), nil
}

// rtpDropInterceptor drops the first rtp packet of marked frames
type rtpDropInterceptor struct {
}

func newRtpDropInterceptor() *rtpDropInterceptor {
	return &rtpDropInterceptor{}
}

func (i *rtpDropInterceptor) Link(w Writer, _ Info) (Writer, error) {
	return WriterFunc(func(b []byte, a Attributes) error {
		shouldDrop := false
		if drop, ok := a["DROP"]; ok {
			if dropBool, ok := drop.(bool); ok && dropBool {
				shouldDrop = true
			}
		}

		// parse RTP packet to detect frame start
		pkt := new(rtp.Packet)
		if err := pkt.Unmarshal(b); err == nil {
			var vp8 codecs.VP8Packet
			if _, err := vp8.Unmarshal(pkt.Payload); err == nil {
				if vp8.S == 1 && shouldDrop {
					// first packet of marked frame -> drop it
					return nil
				}
			}
		}

		return w.Write(b, a)
	}), nil
}
