package quictransport

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync/atomic"
	"time"

	"github.com/Willi-42/go-nada/nada"
	"github.com/mengelbart/mrtp/datachannels"
	"github.com/pion/bwe/gcc"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/quicvarint"
)

type Option func(*Transport) error

// Transport is a quic connection that can be real-time congestion controlled.
// Opens a quic datachannel connection over it for the feedback.
// Only works with application data that use quicdc or roq format.
type Transport struct {
	ctx           context.Context
	netConn       net.PacketConn
	quicTransport *quic.Transport
	quicConn      *quic.Conn
	role          Role
	localAddress  string
	remoteAddress string

	running atomic.Bool

	dcTransport *datachannels.Transport

	nada *nada.SenderOnly
	bwe  *gcc.SendSideController

	qlogFile string

	SetSourceTargetRate func(ratebps uint) error
	HandleUintStream    func(flowID uint64, rs *quic.ReceiveStream)
	HandleDatagram      func(flowID uint64, datagram []byte)
}

func EnableNADA(initRate, minRate, maxRate, expectedFeedbackDelta uint) Option {
	return func(t *Transport) error {
		nadaConfig := nada.Config{
			MinRate:                  uint64(minRate),
			MaxRate:                  uint64(maxRate),
			StartRate:                uint64(initRate),
			FeedbackDelta:            uint64(expectedFeedbackDelta), // ms
			DeactivateQDelayWrapping: true,
			RefCongLevel:             15, // ms
		}

		nadaSo := nada.NewSenderOnly(nadaConfig)
		t.nada = &nadaSo
		return nil
	}
}

func EnableGCC(initRate, minRate, maxRate int) Option {
	return func(t *Transport) error {
		// TODO: add pion logger
		var err error
		t.bwe, err = gcc.NewSendSideController(initRate, minRate, maxRate)
		return err
	}
}

func WithRole(r Role) Option {
	return func(t *Transport) error {
		t.role = r
		return nil
	}
}

func SetNetConn(net net.PacketConn) Option {
	return func(t *Transport) error {
		t.netConn = net
		return nil
	}
}

func SetLocalAddress(address string, port uint) Option {
	return func(t *Transport) error {
		t.localAddress = fmt.Sprintf("%s:%d", address, port)
		return nil
	}
}

func SetRemoteAddress(address string, port uint) Option {
	return func(t *Transport) error {
		t.remoteAddress = fmt.Sprintf("%s:%d", address, port)
		return nil
	}
}

func EnableQLogs(qlogFile string) Option {
	return func(t *Transport) error {
		t.qlogFile = qlogFile
		return nil
	}
}

func New(ctx context.Context, tlsNextProtos []string, opts ...Option) (*Transport, error) {
	t := &Transport{
		role: RoleServer,
		ctx:  ctx,
	}

	for _, opt := range opts {
		if err := opt(t); err != nil {
			return nil, err
		}
	}

	tracer := &tracerFactory{
		qlogFileName: t.qlogFile,
	}

	if t.role == RoleServer {
		quicConfig := &quic.Config{
			EnableDatagrams:                true,
			InitialStreamReceiveWindow:     quicvarint.Max,
			InitialConnectionReceiveWindow: quicvarint.Max,
			MaxIncomingUniStreams:          quicvarint.Max,
			Tracer:                         tracer.newTracer,
		}

		var err error
		if t.netConn != nil {
			t.quicTransport, t.quicConn, err = OpenServerConnWithNet(ctx, quicConfig, tlsNextProtos, t.netConn)
		} else {
			t.quicConn, err = OpenServerConn(ctx, t.localAddress, quicConfig, tlsNextProtos)
		}
		if err != nil {
			return nil, err
		}
	} else {
		quicConfig := &quic.Config{
			EnableDatagrams:                true,
			InitialStreamReceiveWindow:     quicvarint.Max,
			InitialConnectionReceiveWindow: quicvarint.Max,
			MaxIncomingUniStreams:          quicvarint.Max,
			Tracer:                         tracer.newTracer,
		}

		var err error
		if t.netConn != nil {
			t.quicTransport, t.quicConn, err = OpenClientConnWithNet(ctx, t.remoteAddress, quicConfig, tlsNextProtos, t.netConn)
		} else {
			t.quicConn, err = OpenClientConn(ctx, t.remoteAddress, quicConfig, tlsNextProtos)
		}
		if err != nil {
			return nil, err
		}
	}

	t.running.Store(true)

	return t, nil
}

func (t *Transport) GetRTT() time.Duration {
	return t.quicConn.ConnectionStats().LatestRTT
}

func (t *Transport) StartHandlers() {
	go t.receiveDatagrams()
	go t.receiveUniStreams() // already opened feedback stream; do not have to worry about that here
}

// GetQuicDataChannel returns the underlying quic connection.
func (t *Transport) GetQuicConnection() *quic.Conn {
	return t.quicConn
}

// Close shuts down the transport and all associated goroutines.
func (t *Transport) Close() {
	t.running.Store(false)
	if t.quicConn != nil {
		t.quicConn.CloseWithError(0, "bye")
	}
	if t.quicTransport != nil {
		t.quicTransport.Close()
	}
}

// GetQuicDataChannel returns the datachannel connection that was opend for the feedback.
func (t *Transport) GetQuicDataChannel() *datachannels.Transport {
	return t.dcTransport
}

func (t *Transport) receiveUniStreams() {
	for t.running.Load() {
		rs, err := t.quicConn.AcceptUniStream(t.ctx)
		if err != nil {
			slog.Error("Error in receiveUniStreams:", "error", err)
			return
		}

		// read flowID
		reader := quicvarint.NewReader(rs)
		flowID, err := quicvarint.Read(reader)
		if err != nil {
			var streamErr *quic.StreamError
			if errors.As(err, &streamErr) {
				// Stream was canceled; nothing to do
				continue
			}
			panic(err)
		}

		go func() {
			if t.HandleUintStream != nil {
				t.HandleUintStream(flowID, rs)
			}
		}()
	}
}

func (t *Transport) receiveDatagrams() {
	for t.running.Load() {
		dgram, err := t.quicConn.ReceiveDatagram(t.ctx)
		if err != nil {
			slog.Error("Error in receiveDatagrams:", "error", err)
			return
		}

		// read flowID
		flowID, _, err := quicvarint.Parse(dgram)
		if err != nil {
			panic(err)
		}

		if t.HandleDatagram != nil {
			t.HandleDatagram(flowID, dgram)
		}
	}
}
