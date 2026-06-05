//go:build go1.25 && simulation

package simulation

import (
	"context"
	"fmt"
	"log/slog"
	"net/netip"
	"path/filepath"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mengelbart/mrtp"
	"github.com/mengelbart/mrtp/gopipe"
	"github.com/mengelbart/mrtp/gopipe/codec"
	"github.com/mengelbart/mrtp/internal/quictransport"
	"github.com/mengelbart/mrtp/roq"
	"github.com/mengelbart/netsim"
	roqProtocol "github.com/mengelbart/roq"
	"github.com/quic-go/quic-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFakeCodecGCC(t *testing.T) {
	bwe, err := mrtp.NewGCC(1_000_000, 400_000, 8_000_000)
	require.NoError(t, err)
	testFakeCodec(t, bwe)
}

func TestFakeCodecNada(t *testing.T) {
	bwe := mrtp.NewNada(1_000_000, 400_000, 8_000_000, 20*time.Millisecond)
	testFakeCodec(t, bwe)
}

func testFakeCodec(t *testing.T, bwe mrtp.BWE) {
	err := initTestResultDir()
	require.NoError(t, err)

	err = createFakeConfig()
	require.NoError(t, err)

	logFile := configureLogging()
	defer logFile.Close()

	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// just a single network config for now
		bw := float64(5_000_000) // bit/s
		owd := 10 * time.Millisecond
		bdp := int(2 * bw * owd.Seconds())

		forward := pathFactoryFunc(owd, bw, 5000, bdp, false)
		backward := pathFactoryFunc(owd, bw, 5000, bdp, false)

		net := netsim.NewNet(forward(), backward())

		err = net.WriteTcLogForwardPath(filepath.Join(RESULT_DIR, "tc.log"), 100*time.Second)
		assert.NoError(t, err)

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
		clientTransport, err = createSender(ctx, clientConn, bwe)
		assert.NoError(t, err)
		assert.NotNil(t, clientTransport)

		// give them time to connect
		time.Sleep(100 * time.Millisecond)

		println("conn established; start send/receive")

		// all connected, start sender and receiver
		wg.Go(func() {
			err = runFakeReceiver(ctx, serverTransport, &wg)
			assert.NoError(t, err)
			println("receiver ended")
		})

		err = runFakeSender(ctx, clientTransport)
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

func runFakeSender(ctx context.Context, quicConn *quictransport.Transport) error {
	// open roq connection
	roqTransport, err := roq.New(ctx, quicConn.GetQuicConnection())
	if err != nil {
		return err
	}

	// set handlers for datagrams and streams
	quicConn.HandleDatagram = func(flowID uint64, dgram []byte) {
		// all datagrams belong to RoQ for now
		roqTransport.HandleDatagram(dgram)
	}
	quicConn.HandleUniStream = func(flowID uint64, rs *quic.ReceiveStream) {
		if flowID == uint64(rtpFlowID) || flowID == uint64(rtcpRecvFlowID) || flowID == uint64(rtcpSendFlowID) {
			roqTransport.HandleUniStreamWithFlowID(flowID, roqProtocol.NewQuicGoReceiveStream(rs))
			return
		}

		panic(fmt.Sprint("unknown stream flowID ", flowID))
	}
	quicConn.StartHandlers()

	rtpSink, err := roqTransport.NewSendFlow(uint64(rtpFlowID), roq.SendModeSingleStream, true)
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

	appSink := gopipe.WriterFunc(func(b []byte, _ gopipe.Attributes) error {
		_, err := rtpSink.Write(b)
		return err
	})

	fakeSource := gopipe.NewFakeSource(100*time.Second, 250_000, 8_000_000, 750_000)
	defer fakeSource.Close()

	i := fakeSource.GetInfo()

	// set rate callbacks
	quicConn.SetSourceTargetRate = func(ratebps uint) error {
		slog.Info("NEW_TARGET_RATE", "rate", ratebps)

		fakeSource.SetTargetRate(uint64(ratebps))

		return nil
	}

	packetizer := &gopipe.RTPPacketizerFactory{
		MTU:       1420,
		PT:        96,
		SSRC:      0,
		ClockRate: 90_000,
		Codec:     codec.FAKE,
	}
	pacer := gopipe.NewFrameSpacer(ctx)
	defer pacer.Close()

	rtpPipeline, err := gopipe.Chain(i, appSink, pacer, packetizer)
	if err != nil {
		return err
	}

	return fakeSource.StartLive(ctx, rtpPipeline)
}

func runFakeReceiver(ctx context.Context, quicConn *quictransport.Transport, wg *sync.WaitGroup) error {
	roqTransport, err := roq.New(ctx, quicConn.GetQuicConnection())
	if err != nil {
		return err
	}
	defer roqTransport.Close()

	// set handlers for datagrams and streams
	// have to forward it ether to roq or dc
	quicConn.HandleDatagram = func(flowID uint64, dgram []byte) {
		roqTransport.HandleDatagram(dgram)
	}
	quicConn.HandleUniStream = func(flowID uint64, rs *quic.ReceiveStream) {
		if flowID == uint64(rtpFlowID) || flowID == uint64(rtcpRecvFlowID) || flowID == uint64(rtcpSendFlowID) {
			roqTransport.HandleUniStreamWithFlowID(flowID, roqProtocol.NewQuicGoReceiveStream(rs))
			return
		}

		panic(fmt.Sprint("unknown stream flowID ", flowID))
	}

	// start handler
	quicConn.StartHandlers()

	rtpSrc, err := roqTransport.NewReceiveFlow(uint64(rtpFlowID), true)
	if err != nil {
		return err
	}
	defer rtpSrc.Close()

	fakeSink, err := gopipe.NewFakeSink()
	if err != nil {
		return err
	}

	maxTimeout := 150 * time.Millisecond
	depacketizer, err := gopipe.NewRTPDepacketizer(maxTimeout, codec.FAKE)
	if err != nil {
		return err
	}
	defer depacketizer.Close()

	rtpPipeline, err := gopipe.Chain(gopipe.Info{}, fakeSink, depacketizer)
	if err != nil {
		return err
	}

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

		depacketizer.UpdateRTT(quicConn.GetRTT())

		n, err := rtpSrc.Read(buf)
		if err != nil {
			if err == context.Canceled {
				return nil
			}

			println("receiver: read error:", err)
			return err
		}

		err = rtpPipeline.Write(buf[:n], gopipe.Attributes{})
		if err != nil {
			return err
		}
	}
}
