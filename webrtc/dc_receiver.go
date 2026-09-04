package webrtc

import (
	"context"

	"github.com/mengelbart/mrtp"
	"github.com/mengelbart/mrtp/pipeline"
	"github.com/pion/webrtc/v4"
)

// DCreceiver pushes the messages of a data channel downstream, one chunk per
// message. It is the driver of its segment.
type DCreceiver struct {
	dc      *webrtc.DataChannel
	msgChan chan webrtc.DataChannelMessage

	down mrtp.Sink[mrtp.DataChunk]
	pool *pipeline.Pool[mrtp.DataChunk]
}

func newReceiver(dc *webrtc.DataChannel) *DCreceiver {
	msgChan := make(chan webrtc.DataChannelMessage, 10)
	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		msgChan <- msg
	})

	return &DCreceiver{
		dc:      dc,
		msgChan: msgChan,
		pool: pipeline.NewPool(
			func() *mrtp.DataChunk { return &mrtp.DataChunk{} },
			func(c *mrtp.DataChunk) { c.Data = c.Data[:0] },
		),
	}
}

// Format implements mrtp.Source.
func (r *DCreceiver) Format() mrtp.Format {
	return mrtp.Data{}
}

// Connect implements mrtp.Source.
func (r *DCreceiver) Connect(s mrtp.Sink[mrtp.DataChunk]) error {
	r.down = s
	return nil
}

// Run implements mrtp.Driver.
func (r *DCreceiver) Run(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case msg := <-r.msgChan:
			packet := r.pool.Get()
			chunk := packet.Value()
			chunk.Data = append(chunk.Data[:0], msg.Data...)
			if err := r.down.Write(packet); err != nil {
				return err
			}
		}
	}
}

func (r *DCreceiver) Close() error {
	return r.dc.Close()
}

var (
	_ mrtp.Source[mrtp.DataChunk] = (*DCreceiver)(nil)
	_ mrtp.Driver                 = (*DCreceiver)(nil)
)
