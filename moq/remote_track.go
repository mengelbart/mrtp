package moq

import (
	"context"
	"io"
	"log/slog"
	"sync"

	"github.com/mengelbart/moqtransport"
)

type remoteTrack struct {
	track  *moqtransport.RemoteTrack
	wg     sync.WaitGroup
	ctx    context.Context
	cancel context.CancelFunc
	reader io.Reader
	writer *io.PipeWriter
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
			slog.Info("remote track read stopped", "error", err)
			t.writer.CloseWithError(err)
			return
		}
		// TODO: Implement reorder buffer
		if _, err = t.writer.Write(o.Payload); err != nil {
			slog.Error("failed to write remote track payload", "error", err)
			t.writer.CloseWithError(err)
			return
		}
	}
}
