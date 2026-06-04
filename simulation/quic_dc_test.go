//go:build go1.25 && simulation

package simulation

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mengelbart/mrtp"
	"github.com/mengelbart/mrtp/data"
	"github.com/mengelbart/mrtp/datachannels"
	"github.com/mengelbart/mrtp/internal/quictransport"
	"github.com/mengelbart/netsim"
	"github.com/quic-go/quic-go"
	"github.com/stretchr/testify/assert"
)

func TestQUICdc(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// just a single network config for now
		bw := float64(1_250_000) // bit/s
		owd := 50 * time.Millisecond
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
		bwe := mrtp.NewNada(750_000, 150_000, 8_000_000, 20*time.Millisecond)
		clientTransport, err = createSender(ctx, clientConn, bwe)
		assert.NoError(t, err)
		assert.NotNil(t, clientTransport)

		// give them time to connect
		time.Sleep(100 * time.Millisecond)

		// all connected, start sender and receiver
		wg.Go(func() {
			err = runDcReceiver(t, &wg, serverTransport)
			assert.NoError(t, err)
		})

		runDcSender(t, ctx, clientTransport)

		// give sink time to receive everything
		time.Sleep(300 * time.Millisecond)

		// shut down transports
		if clientTransport != nil {
			clientTransport.Close()
		}
		if serverTransport != nil {
			serverTransport.Close()
		}

		time.Sleep(100 * time.Millisecond)

		// Cancel context to signal shutdown
		cancel()
		wg.Wait()
		net.Close()
		synctest.Wait()
	})
}

func createSender(ctx context.Context, conn net.PacketConn, bwe mrtp.BWE) (*quictransport.Transport, error) {
	quicTOptions := []quictransport.Option{
		quictransport.WithRole(quictransport.Role(quictransport.RoleClient)),
		quictransport.SetRemoteAddress("10.0.0.1", 8080),
		quictransport.SetNetConn(conn),
		quictransport.SetBWE(bwe),
		quictransport.SetQLOGLabel("sender"),
	}

	return quictransport.New(ctx, []string{"dc"}, quicTOptions...)
}

func createReceiver(ctx context.Context, conn net.PacketConn) (*quictransport.Transport, error) {
	quicOptions := []quictransport.Option{
		quictransport.WithRole(quictransport.Role(quictransport.RoleServer)),
		quictransport.SetNetConn(conn),
		quictransport.SetQLOGLabel("receiver"),
	}

	return quictransport.New(ctx, []string{"dc"}, quicOptions...)
}

func runDcSender(t *testing.T, ctx context.Context, quicConn *quictransport.Transport) error {
	dcTransport, err := datachannels.New(quicConn.GetQuicConnection())
	if err != nil {
		return err
	}

	// set handlers for datagrams and streams
	quicConn.HandleDatagram = func(flowID uint64, dgram []byte) {
		// no datagrams expected
	}
	quicConn.HandleUniStream = func(flowID uint64, rs *quic.ReceiveStream) {
		err := dcTransport.ReadStream(context.Background(), datachannels.NewQuicGoReceiveStream(rs), flowID)
		if err != nil {
			panic(fmt.Sprintf("forward stream with flowID: %v: %v", flowID, err))
		}
	}
	quicConn.StartHandlers()

	// blocks until we get OpenChannelOk
	sender, err := dcTransport.NewDataChannelSender(uint64(dataChannelFlowID), 0, true)
	assert.NoError(t, err)

	opts := []data.DataBinOption{
		data.UseRateLimiter(750_000, 10000),
		data.UseChunkSource(),
	}

	source, err := data.NewDataBin(sender, opts...)
	assert.NoError(t, err)

	// rate is controlled by cc
	quicConn.SetSourceTargetRate = func(ratebps uint) error {
		// log "combined" target rate even if we do not split it. Makes plotting easier
		slog.Info("NEW_TARGET_RATE", "rate", ratebps)

		source.SetRateLimit(ratebps)
		return nil
	}

	return source.Run(ctx)
}

func runDcReceiver(t *testing.T, wg *sync.WaitGroup, quicConn *quictransport.Transport) error {
	dcTransport, err := datachannels.New(quicConn.GetQuicConnection())
	if err != nil {
		return err
	}

	// start handler
	quicConn.StartHandlers()

	wg.Go(func() {
		receiver, err := dcTransport.AddDataChannelReceiver(uint64(dataChannelFlowID))
		assert.NoError(t, err)
		assert.NotNil(t, receiver)

		sink, err := data.NewSink(receiver)
		assert.NoError(t, err)
		assert.NotNil(t, sink)

		err = sink.Run()
		assert.Equal(t, err, io.EOF)
	})

	// set handlers for datagrams and streams
	quicConn.HandleDatagram = func(flowID uint64, dgram []byte) {
		// no datagrams expected
	}

	quicConn.HandleUniStream = func(flowID uint64, rs *quic.ReceiveStream) {
		err := dcTransport.ReadStream(context.Background(), datachannels.NewQuicGoReceiveStream(rs), flowID)
		if err != nil {
			panic(fmt.Sprintf("forward stream with flowID: %v: %v", flowID, err))
		}
	}

	return nil
}
