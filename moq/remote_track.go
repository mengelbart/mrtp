package moq

import (
	"context"
	"io"
	"sync"

	"github.com/mengelbart/moqtransport"
)

type remoteTrack struct {
	track  *moqtransport.RemoteTrack
	wg     sync.WaitGroup
	ctx    context.Context
	cancel context.CancelFunc
	reader io.Reader
	writer io.Writer
}

func newRemoteTrack(track *moqtransport.RemoteTrack) (*remoteTrack, error) {
	ctx, cancel := context.WithCancel(context.Background())
	r, w := io.Pipe()
	rt := &remoteTrack{
		track:  track,
		wg:     sync.WaitGroup{},
		ctx:    ctx,
		cancel: cancel,
		reader: r,
		writer: w,
	}
	rt.wg.Go(rt.run)
	return rt, nil
}

func (t *remoteTrack) Read(buf []byte) (int, error) {
	return t.reader.Read(buf)
}

func (t *remoteTrack) run() {
	for {
		o, err := t.track.ReadObject(t.ctx)
		if err != nil {
			// TODO: Handle different error cases
			panic(err)
		}
		// TODO: Implement reorder buffer
		if _, err = t.writer.Write(o.Payload); err != nil {
			// TODO: How to handle?
			panic(err)
		}
	}
}

func (t *remoteTrack) close() {
	t.cancel()
	t.wg.Wait()
}
