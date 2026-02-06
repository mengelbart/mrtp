package codec

import (
	"context"
	"io"
	"log/slog"
	"os"
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
		depacketizer := NewRTPDepacketizer(func(frame []byte) {
			slog.Info("got frame", "size", len(frame))
			framesReceived++
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

		depacketizer := NewRTPDepacketizer(func(frame []byte) {
			frameCopy := make([]byte, len(frame))
			copy(frameCopy, frame)
			receivedFrames = append(receivedFrames, frameCopy)
			slog.Info("received frame", "size", len(frame), "count", len(receivedFrames))
		})
		defer depacketizer.Close()

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

		inter := newInterceptor()

		writer, err := Chain(i, sink, pacer, packetizer, inter, encoder)
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

		// Wait a bit for async processing
		time.Sleep(100 * time.Millisecond)

		// Verify frame counts match
		assert.Equal(t, maxFrames, len(inter.sentFrames), "should have captured %d frames", maxFrames)
		assert.Equal(t, maxFrames, len(receivedFrames), "should have received %d frames", maxFrames)
		assert.Equal(t, len(inter.sentFrames), len(receivedFrames), "sent and received frame counts should match")

		// Compare each frame
		for i := 0; i < len(inter.sentFrames); i++ {
			assert.Equal(t, len(inter.sentFrames[i]), len(receivedFrames[i]),
				"frame %d: length mismatch", i)
			assert.Equal(t, inter.sentFrames[i], receivedFrames[i],
				"frame %d: content mismatch", i)
			slog.Info("frame comparison", "index", i, "size", len(inter.sentFrames[i]), "match", true)
		}

		synctest.Wait()
	})
}

type interceptor struct {
	sentFrames [][]byte
}

func newInterceptor() *interceptor {
	return &interceptor{
		sentFrames: make([][]byte, 0),
	}
}

func (i *interceptor) Link(w Writer, _ Info) (Writer, error) {
	return WriterFunc(func(b []byte, a Attributes) error {
		frameCopy := make([]byte, len(b))
		copy(frameCopy, b)
		i.sentFrames = append(i.sentFrames, frameCopy)
		// slog.Info("captured frame", "size", len(b), "count", len(i.sentFrames))

		return w.Write(b, a)
	}), nil
}
