package codec

import (
	"context"
	"io"
	"log/slog"
	"os"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mengelbart/y4m"
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
		depacketizer := newRTPDepacketizer(timeout, func(frame []byte) {
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

		reader, streamHeader, err := y4m.NewReader(file)
		assert.NoError(t, err)

		i := Info{
			Width:       uint(streamHeader.Width),
			Height:      uint(streamHeader.Height),
			TimebaseNum: streamHeader.FrameRate.Numerator,
			TimebaseDen: streamHeader.FrameRate.Denominator,
		}
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
		writer, err := Chain(i, sink, pacer, packetizer, encoder)
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
		depacketizer := newRTPDepacketizer(timeout, func(frame []byte) {
			frameCopy := make([]byte, len(frame))
			copy(frameCopy, frame)
			receivedFrames = append(receivedFrames, frameCopy)
			slog.Info("received frame", "size", len(frame), "count", len(receivedFrames))
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

		reader, streamHeader, err := y4m.NewReader(file)
		assert.NoError(t, err)

		i := Info{
			Width:       uint(streamHeader.Width),
			Height:      uint(streamHeader.Height),
			TimebaseNum: streamHeader.FrameRate.Numerator,
			TimebaseDen: streamHeader.FrameRate.Denominator,
		}

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

		fInter := newFrameInterceptor()

		writer, err := Chain(i, sink, pacer, packetizer, fInter, encoder)
		assert.NoError(t, err)

		fps := float64(i.TimebaseNum) / float64(i.TimebaseDen)
		frameDuration := time.Duration(float64(time.Second) / fps)

		ticker := time.NewTicker(frameDuration)
		defer ticker.Stop()

		framesSent := 0
		for range ticker.C {
			if framesSent >= maxFrames {
				break
			}

			frame, _, err := reader.ReadNextFrame()
			if err != nil {
				if err == io.EOF {
					break
				}
				assert.NoError(t, err)
				break
			}

			csr := convertSubsampleRatio(streamHeader.ChromaSubsampling)
			err = writer.Write(frame, Attributes{
				ChromaSubsampling: csr,
			})
			assert.NoError(t, err)

			framesSent++
		}

		time.Sleep(100 * time.Millisecond)

		// verify frame counts match
		assert.Equal(t, maxFrames, len(fInter.sentFrames), "should have captured %d frames", maxFrames)
		assert.Equal(t, maxFrames, len(receivedFrames), "should have received %d frames", maxFrames)
		assert.Equal(t, len(fInter.sentFrames), len(receivedFrames), "sent and received frame counts should match")

		// compare each frame
		for i := 0; i < len(fInter.sentFrames); i++ {
			assert.Equal(t, len(fInter.sentFrames[i]), len(receivedFrames[i]),
				"frame %d: length mismatch", i)
			assert.Equal(t, fInter.sentFrames[i], receivedFrames[i],
				"frame %d: content mismatch", i)
			slog.Info("frame comparison", "index", i, "size", len(fInter.sentFrames[i]), "match", true)
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

		receivedFrames := make([][]byte, 0, maxFrames)

		timeout := 10 * time.Millisecond
		depacketizer := newRTPDepacketizer(timeout, func(frame []byte) {
			frameCopy := make([]byte, len(frame))
			copy(frameCopy, frame)
			receivedFrames = append(receivedFrames, frameCopy)
			slog.Info("received frame", "size", len(frame), "count", len(receivedFrames))
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

		reader, streamHeader, err := y4m.NewReader(file)
		assert.NoError(t, err)

		i := Info{
			Width:       uint(streamHeader.Width),
			Height:      uint(streamHeader.Height),
			TimebaseNum: streamHeader.FrameRate.Numerator,
			TimebaseDen: streamHeader.FrameRate.Denominator,
		}

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

		fInter := newFrameInterceptor()
		dropInter := newRtpDropInterceptor([]uint16{5, 444})

		writer, err := Chain(i, sink, pacer, dropInter, packetizer, fInter, encoder)
		assert.NoError(t, err)

		fps := float64(i.TimebaseNum) / float64(i.TimebaseDen)
		frameDuration := time.Duration(float64(time.Second) / fps)

		ticker := time.NewTicker(frameDuration)
		defer ticker.Stop()

		framesSent := 0
		for range ticker.C {
			if framesSent >= maxFrames {
				break
			}

			frame, _, err := reader.ReadNextFrame()
			if err != nil {
				if err == io.EOF {
					break
				}
				assert.NoError(t, err)
				break
			}

			csr := convertSubsampleRatio(streamHeader.ChromaSubsampling)
			err = writer.Write(frame, Attributes{
				ChromaSubsampling: csr,
			})
			assert.NoError(t, err)

			framesSent++
		}

		time.Sleep(100 * time.Millisecond)

		// verify frame counts match
		assert.Equal(t, maxFrames, len(fInter.sentFrames), "should have captured %d frames", maxFrames)
		assert.Equal(t, maxFrames-2, len(receivedFrames), "should have received %d frames", maxFrames-2)

		depacketizer.Close()
		wg.Wait()
		synctest.Wait()
	})
}

type frameInterceptor struct {
	sentFrames [][]byte
}

func newFrameInterceptor() *frameInterceptor {
	return &frameInterceptor{
		sentFrames: make([][]byte, 0),
	}
}

func (i *frameInterceptor) Link(w Writer, _ Info) (Writer, error) {
	return WriterFunc(func(b []byte, a Attributes) error {
		frameCopy := make([]byte, len(b))
		copy(frameCopy, b)
		i.sentFrames = append(i.sentFrames, frameCopy)
		// slog.Info("captured frame", "size", len(b), "count", len(i.sentFrames))

		return w.Write(b, a)
	}), nil
}

type rtpDropInterceptor struct {
	toDrop    []uint16
	packetCnt uint16
}

func newRtpDropInterceptor(toDrop []uint16) *rtpDropInterceptor {
	return &rtpDropInterceptor{
		toDrop: toDrop,
	}
}

func (i *rtpDropInterceptor) Link(w Writer, _ Info) (Writer, error) {
	return WriterFunc(func(b []byte, a Attributes) error {
		defer func() { i.packetCnt++ }()
		if slices.Contains(i.toDrop, i.packetCnt) {
			// drop packet
			return nil
		}

		return w.Write(b, a)
	}), nil
}
