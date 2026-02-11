package simulation

import (
	"context"
	"fmt"
	"image"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mengelbart/mrtp/codec"
	"github.com/mengelbart/mrtp/data"
	"github.com/mengelbart/mrtp/flags"
	"github.com/mengelbart/mrtp/internal/quictransport"
	"github.com/mengelbart/mrtp/roq"
	"github.com/mengelbart/netsim"
	roqProtocol "github.com/mengelbart/roq"
	"github.com/mengelbart/y4m"
	"github.com/quic-go/quic-go"
	"github.com/stretchr/testify/assert"
)

func TestQUICvp8(t *testing.T) {
	// video file must exist
	if _, err := os.Stat("Johnny_1280x720_60.y4m"); os.IsNotExist(err) {
		println("Video file not found: Johnny_1280x720_60.y4m - run ./get-video.sh to download it.\n")
		t.Skip("video not found")
	}

	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// just a single network config for now
		bw := float64(1_250_000) // bit/s
		owd := 20 * time.Millisecond
		bdp := int(2 * bw * owd.Seconds())

		forward := pathFactoryFunc(owd, bw, 5000, bdp, false)
		backward := pathFactoryFunc(owd, bw, 5000, bdp, false)

		net := netsim.NewNet(forward(), backward())

		left := net.NIC(netsim.LeftLocation, netip.MustParseAddr("10.0.0.1"))
		serverConn, err := left.ListenPacket("udp", "10.0.0.1:8080")
		assert.NoError(t, err)

		right := net.NIC(netsim.RightLocation, netip.MustParseAddr("10.0.0.2"))
		clientConn, err := right.Dial("udp", "10.0.0.1:8080")
		assert.NoError(t, err)

		var wg sync.WaitGroup
		var serverTransport, clientTransport *quictransport.Transport

		// start server in goroutine
		wg.Go(func() {
			serverTransport, err = createReceiver(ctx, serverConn)
			assert.NoError(t, err)
			assert.NotNil(t, serverTransport)
		})

		// start client in main goroutine
		clientTransport, err = createVp8Sender(ctx, clientConn)
		assert.NoError(t, err)
		assert.NotNil(t, clientTransport)

		// give them time to connect
		time.Sleep(100 * time.Millisecond)

		println("conn established; start send/receive")

		// all connected, start sender and receiver
		wg.Go(func() {
			err = runVp8Receiver(ctx, serverTransport, &wg)
			assert.NoError(t, err)
			println("receiver ended")
		})

		err = runVp8Sender(ctx, clientTransport)
		assert.NoError(t, err)

		time.Sleep(20 * time.Second)
		println("closing everything")

		if clientTransport != nil {
			clientTransport.Close()
		}
		if serverTransport != nil {
			serverTransport.Close()
		}

		// give receiver time to process last packets
		time.Sleep(5 * time.Second)

		cancel()

		// give goroutines time to see context cancellation
		time.Sleep(100 * time.Millisecond)

		wg.Wait()
		net.Close()
		synctest.Wait()
	})
}

func createVp8Sender(ctx context.Context, conn net.PacketConn) (*quictransport.Transport, error) {
	quicTOptions := []quictransport.Option{
		quictransport.WithRole(quictransport.Role(quictransport.RoleClient)),
		quictransport.SetQuicCC(0), // reno
		quictransport.WithPacer(0), // TODO: rate-bassed pacer does not work yet in synctest
		quictransport.SetRemoteAddress("10.0.0.1", 8080),
		quictransport.SetNetConn(conn),
		quictransport.EnableNADA(750_000, 150_000, 8_000_000, uint(20), uint64(flags.NadaFeedbackFlowID)),
		quictransport.EnableQLogs("./sender.qlog"),
	}

	return quictransport.New(ctx, []string{"dc"}, quicTOptions...)
}

func runVp8Sender(ctx context.Context, quicConn *quictransport.Transport) error {
	// open roq connection
	roqOpt := []roq.Option{roq.EnableRoqLogs("sender.roq.qlog")}
	roqTransport, err := roq.New(ctx, quicConn.GetQuicConnection(), roqOpt...)
	if err != nil {
		return err
	}

	// set handlers for datagrams and streams
	quicConn.HandleDatagram = func(flowID uint64, dgram []byte) {
		// all datagrams belong to RoQ for now
		roqTransport.HandleDatagram(dgram)
	}
	quicConn.HandleUintStream = func(flowID uint64, rs *quic.ReceiveStream) {
		if flowID == uint64(flags.RTPFlowID) || flowID == uint64(flags.RTCPRecvFlowID) || flowID == uint64(flags.RTCPSendFlowID) {
			roqTransport.HandleUniStreamWithFlowID(flowID, roqProtocol.NewQuicGoReceiveStream(rs))
			return
		}

		panic(fmt.Sprint("unknown stream flowID ", flowID))
	}
	quicConn.StartHandlers()

	rtpSink, err := roqTransport.NewSendFlow(uint64(flags.RTPFlowID), roq.SendMode(1), flags.TraceRTPSend)
	if err != nil {
		return err
	}

	defer func() {
		println("closing sender")

		// give pacer time to send everything
		time.Sleep(5 * time.Second)
		rtpSink.Close()
		roqTransport.Close()
		roqTransport.CloseLogFile()
	}()

	sink := codec.WriterFunc(func(b []byte, _ codec.Attributes) error {
		println("Sender write len: ", len(b))
		_, err := rtpSink.Write(b)
		return err
	})

	file, err := os.Open("Johnny_1280x720_60.y4m")
	if err != nil {
		return err
	}
	defer file.Close()

	reader, streamHeader, err := y4m.NewReader(file)
	if err != nil {
		return err
	}

	i := codec.Info{
		Width:       uint(streamHeader.Width),
		Height:      uint(streamHeader.Height),
		TimebaseNum: streamHeader.FrameRate.Numerator,
		TimebaseDen: streamHeader.FrameRate.Denominator,
	}
	encoder := codec.NewVP8Encoder()

	// set rate callbacks
	quicConn.SetSourceTargetRate = func(ratebps uint) error {
		slog.Info("NEW_TARGET_RATE", "rate", ratebps)

		encoder.SetTargetRate(uint64(ratebps))

		return nil
	}

	packetizer := &codec.RTPPacketizerFactory{
		MTU:       1420,
		PT:        96,
		SSRC:      0,
		ClockRate: 90_000,
	}
	pacer := &codec.FrameSpacer{
		Ctx: ctx,
	}
	writer, err := codec.Chain(i, sink, pacer, packetizer, encoder)
	if err != nil {
		return err
	}

	fps := float64(i.TimebaseNum) / float64(i.TimebaseDen)
	frameDuration := time.Duration(float64(time.Second) / fps)

	ticker := time.NewTicker(frameDuration)
	defer ticker.Stop()
	var next time.Time
	for range ticker.C {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		now := time.Now()
		lateness := now.Sub(next)
		next = now.Add(frameDuration)
		slog.Info("FRAME", "duration", frameDuration, "next", now.Add(frameDuration), "lateness", lateness)
		frame, _, err := reader.ReadNextFrame()
		if err != nil {
			if err == io.EOF {
				println("sending done")
				return nil
			}
			return err
		}
		ioDone := time.Now()
		slog.Info("read frame from disk", "latency", ioDone.Sub(now))
		csr := convertSubsampleRatio(streamHeader.ChromaSubsampling)
		if err = writer.Write(frame, codec.Attributes{
			codec.ChromaSubsampling: csr,
		}); err != nil {
			return err
		}
	}

	return nil
}

func runVp8Receiver(ctx context.Context, quicConn *quictransport.Transport, wg *sync.WaitGroup) error {
	roqTransport, err := roq.New(ctx, quicConn.GetQuicConnection())
	if err != nil {
		return err
	}
	defer roqTransport.Close()

	dcTransport := quicConn.GetQuicDataChannel()

	// set handlers for datagrams and streams
	// have to forward it ether to roq or dc
	quicConn.HandleDatagram = func(flowID uint64, dgram []byte) {
		roqTransport.HandleDatagram(dgram)
	}
	quicConn.HandleUintStream = func(flowID uint64, rs *quic.ReceiveStream) {
		if flowID == uint64(flags.RTPFlowID) || flowID == uint64(flags.RTCPRecvFlowID) || flowID == uint64(flags.RTCPSendFlowID) {
			roqTransport.HandleUniStreamWithFlowID(flowID, roqProtocol.NewQuicGoReceiveStream(rs))
			return
		}

		if flags.DataChannel {
			dcTransport.ReadStream(context.Background(), rs, flowID)
			return
		}

		panic(fmt.Sprint("unknown stream flowID ", flowID))
	}

	// start handler
	quicConn.StartHandlers()

	if flags.DataChannel {
		// setup data channel receiver
		// quic tranpsorts has to be started before
		dcReceiver, err := dcTransport.AddDataChannelReceiver(uint64(flags.DataChannelFlowID))
		if err != nil {
			return err
		}

		dataSink, err := data.NewSink(dcReceiver)
		if err != nil {
			return err
		}

		go dataSink.Run()
	}

	rtpSrc, err := roqTransport.NewReceiveFlow(uint64(flags.RTPFlowID), flags.TraceRTPRecv)
	if err != nil {
		return err
	}
	defer rtpSrc.Close()

	timeout := 10 * time.Millisecond
	depacketizer := codec.NewRTPDepacketizer(timeout, func(frame []byte) {
		slog.Info("received frame", "size", len(frame))
	})
	defer depacketizer.Close()

	wg.Go(func() {
		depacketizer.Run()
	})

	wg.Go(func() {
		// end receiver orderly on context cancellation
		<-ctx.Done()
		roqTransport.CloseLogFile()
		roqTransport.Close()
		rtpSrc.Close()
		depacketizer.Close()
	})

	buf := make([]byte, 150000)
	for {
		select {
		case <-ctx.Done():
			println("receiver: context cancelled, exiting")
			return nil
		default:
		}

		n, err := rtpSrc.Read(buf)
		if err != nil {
			if err == context.Canceled {
				return nil
			}

			println("receiver: read error:", err)
			return err
		}

		err = depacketizer.Write(buf[:n])
		if err != nil {
			return err
		}
	}
}

func convertSubsampleRatio(s y4m.ChromaSubsamplingType) image.YCbCrSubsampleRatio {
	switch s {
	case y4m.CST411:
		return image.YCbCrSubsampleRatio411
	case y4m.CST420:
		return image.YCbCrSubsampleRatio420
	case y4m.CST420jpeg:
		return image.YCbCrSubsampleRatio420
	case y4m.CST420mpeg2:
		return image.YCbCrSubsampleRatio420
	case y4m.CST420paldv:
		return image.YCbCrSubsampleRatio420
	case y4m.CST422:
		return image.YCbCrSubsampleRatio422
	case y4m.CST444:
		return image.YCbCrSubsampleRatio444
	case y4m.CST444Alpha:
		return image.YCbCrSubsampleRatio444
	default:
		panic(fmt.Sprintf("unexpected y4m.ChromaSubsamplingType: %#v", s))
	}
}
