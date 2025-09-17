package datachannels

import (
	"context"
	"sync"

	"github.com/mengelbart/quicdc"
	"github.com/quic-go/quic-go"
)

type Transport struct {
	session *quicdc.Session

	quicConn *quic.Conn

	mutex      sync.Mutex
	dcChannels map[uint64]chan *quicdc.DataChannel
}

type Option func(*Transport) error

func New(conn *quic.Conn, opts ...Option) (*Transport, error) {
	t := &Transport{
		dcChannels: make(map[uint64]chan *quicdc.DataChannel),
		mutex:      sync.Mutex{},
		quicConn:   conn,
	}

	for _, opt := range opts {
		if err := opt(t); err != nil {
			return nil, err
		}
	}

	// create quicdc session
	t.session = quicdc.NewSession(t.quicConn)

	t.session.OnIncomingDataChannel(func(dc *quicdc.DataChannel) {
		t.onIncomingDataChannel(dc)
	})

	return t, nil
}

func (t *Transport) NewDataChannelSender(channelID uint64, priority uint64, ordered bool) (*Sender, error) {
	dc, err := t.session.OpenDataChannel(channelID, priority, ordered, 0, "", "")
	if err != nil {
		return nil, err
	}

	return newSender(dc), nil
}

// ReadStream registers a QUIC stream to the quicdc session
func (t *Transport) ReadStream(ctx context.Context, stream *quic.ReceiveStream, channelID uint64) error {
	return t.session.ReadStream(ctx, stream, channelID)
}

func (t *Transport) AddDataChannelReceiver(channelID uint64) (*Receiver, error) {
	var dcChan chan *quicdc.DataChannel

	t.mutex.Lock()
	if ch, ok := t.dcChannels[channelID]; ok {
		dcChan = ch
	} else {
		dcChan = make(chan *quicdc.DataChannel)
		t.dcChannels[channelID] = dcChan
	}
	t.mutex.Unlock()

	// wating for data channel from callback
	dc := <-dcChan

	return newReceiver(dc), nil
}

// onIncomingDataChannel callback for new data channels
func (t *Transport) onIncomingDataChannel(dc *quicdc.DataChannel) {
	t.mutex.Lock()
	defer t.mutex.Unlock()

	if ch, ok := t.dcChannels[dc.ID()]; ok {
		ch <- dc
	} else {
		// create new chan
		dcChan := make(chan *quicdc.DataChannel)
		t.dcChannels[dc.ID()] = dcChan
		dcChan <- dc
	}
}
