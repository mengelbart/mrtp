package datachannels

import (
	"context"
	"log/slog"
	"sync"

	"github.com/mengelbart/quicdc"
	"github.com/quic-go/quic-go"
)

type Transport struct {
	session *quicdc.Session

	quicConn *quic.Conn

	logger              *slog.Logger
	maxReorderBufferLen int

	mutex      sync.Mutex
	dcChannels map[uint64]chan *quicdc.DataChannel
}

type Option func(*Transport) error

// SetLogger sets the logger the quicdc session and its data channels write to.
func SetLogger(logger *slog.Logger) Option {
	return func(t *Transport) error {
		t.logger = logger
		return nil
	}
}

// SetMaxReorderBufferLen bounds how many out of order messages an ordered data
// channel buffers, and sizes the window an unordered channel uses to detect
// repeated sequence numbers.
func SetMaxReorderBufferLen(n int) Option {
	return func(t *Transport) error {
		t.maxReorderBufferLen = n
		return nil
	}
}

func New(ctx context.Context, conn *quic.Conn, opts ...Option) (*Transport, error) {
	t := &Transport{
		dcChannels: make(map[uint64]chan *quicdc.DataChannel),
		mutex:      sync.Mutex{},
		quicConn:   conn,
		logger:     slog.Default(),
	}

	for _, opt := range opts {
		if err := opt(t); err != nil {
			return nil, err
		}
	}

	sessionOptions := []quicdc.Option{quicdc.WithLogger(t.logger)}
	if t.maxReorderBufferLen > 0 {
		sessionOptions = append(sessionOptions, quicdc.WithMaxReorderBufferLen(t.maxReorderBufferLen))
	}

	quicGoConn := NewQUICGoConnection(t.quicConn)

	// create quicdc session
	t.session = quicdc.NewSession(quicGoConn, sessionOptions...)

	t.session.OnIncomingDataChannel(func(dc *quicdc.DataChannel) {
		t.onIncomingDataChannel(dc)
	})

	go func() {
		<-ctx.Done()
		if err := t.Close(); err != nil {
			t.logger.Error("failed to close data channel session", "error", err)
		}
	}()

	return t, nil
}

// Close closes all data channels of the session and the underlying QUIC
// connection.
func (t *Transport) Close() error {
	return t.session.Close()
}

// NewDataChannelSender opens a data channel and blocks until the peer
// acknowledges it or ctx is done.
func (t *Transport) NewDataChannelSender(ctx context.Context, channelID uint64, priority uint64, ordered bool) (*Sender, error) {
	dc, err := t.session.OpenDataChannel(ctx, channelID, priority, ordered, 0, "", "")
	if err != nil {
		return nil, err
	}

	return newSender(dc), nil
}

// ReadStream registers a QUIC stream to the quicdc session. The QUIC connection
// is managed by the application, so quicdc's own accept loop does not run and
// cannot tear the session down: any error ends the session here instead.
func (t *Transport) ReadStream(ctx context.Context, stream quicdc.ReceiveStream, channelID uint64) error {
	t.logger.Info("new dc stream", "streamID", stream.ID(), "flowID", channelID)

	if err := t.session.ReadStream(ctx, stream, channelID); err != nil {
		if closeErr := t.Close(); closeErr != nil {
			t.logger.Error("failed to close data channel session", "error", closeErr)
		}
		return err
	}
	return nil
}

// AddDataChannelReceiver waits for the peer to open the data channel with the
// given ID. It returns when ctx is done.
func (t *Transport) AddDataChannelReceiver(ctx context.Context, channelID uint64) (*Receiver, error) {
	t.mutex.Lock()
	dcChan, ok := t.dcChannels[channelID]
	if !ok {
		dcChan = make(chan *quicdc.DataChannel, 1)
		t.dcChannels[channelID] = dcChan
	}
	t.mutex.Unlock()

	select {
	case dc := <-dcChan:
		return newReceiver(dc), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// onIncomingDataChannel callback for new data channels
func (t *Transport) onIncomingDataChannel(dc *quicdc.DataChannel) {
	t.mutex.Lock()
	defer t.mutex.Unlock()

	dcChan, ok := t.dcChannels[dc.ID()]
	if !ok {
		dcChan = make(chan *quicdc.DataChannel, 1)
		t.dcChannels[dc.ID()] = dcChan
	}

	select {
	case dcChan <- dc:
	default:
		t.logger.Warn("dropping data channel, ID is already in use", "flowID", dc.ID())
	}
}
