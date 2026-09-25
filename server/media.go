package server

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/mengelbart/mrtp"
	"github.com/mengelbart/mrtp/element/mediafile"
	"github.com/mengelbart/mrtp/internal/sessionmedia"
	"github.com/mengelbart/mrtp/pipeline"
	"github.com/mengelbart/mrtp/signaling"
)

const (
	fakeBitrate = 1_000_000
	sendMTU     = 1200
)

// fakeConfig is the fake media a session sends until it closes.
var fakeConfig = sessionmedia.FakeConfig{
	FPS:    30,
	MTU:    sendMTU,
	Bounds: mrtp.RateBounds{Initial: fakeBitrate, Min: fakeBitrate, Max: fakeBitrate},
}

// media opens the files sessions send and record.
type media struct {
	// sources holds the IVF files clients may request, nil if there are none.
	sources *os.Root
	// sinks is where received tracks are recorded, nil to drop them.
	sinks *os.Root
}

func openMedia(sourceDir, sinkDir string) (*media, error) {
	m := &media{}
	var err error
	if sourceDir != "" {
		if m.sources, err = os.OpenRoot(sourceDir); err != nil {
			return nil, err
		}
	}
	if sinkDir != "" {
		if m.sinks, err = os.OpenRoot(sinkDir); err != nil {
			return nil, errors.Join(err, m.Close())
		}
	}
	return m, nil
}

func (m *media) Close() error {
	var errs []error
	for _, root := range []*os.Root{m.sources, m.sinks} {
		if root != nil {
			errs = append(errs, root.Close())
		}
	}
	return errors.Join(errs...)
}

// sender is what a session sends on one stream.
type sender struct {
	codec mrtp.Codec
	// file is the source of a file sender, nil for fake media.
	file *mediafile.IVFSource
}

// newSender opens the source file a client names, or fake media if name is
// empty.
func (m *media) newSender(name string) (*sender, error) {
	if name == "" {
		return &sender{codec: sessionmedia.FakeCodec}, nil
	}
	if m.sources == nil {
		return nil, fmt.Errorf("%w: server has no sources", signaling.ErrBadRequest)
	}
	if !filepath.IsLocal(name) || filepath.Base(name) != name {
		return nil, fmt.Errorf("%w: invalid source name %q", signaling.ErrBadRequest, name)
	}
	file, err := m.sources.Open(name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%w: unknown source %q", signaling.ErrBadRequest, name)
	}
	if err != nil {
		return nil, err
	}
	source, err := mediafile.NewIVFSource(file)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("%w: source %q: %w", signaling.ErrBadRequest, name, err), file.Close())
	}
	return &sender{codec: source.Format().(mrtp.EncodedVideo).Codec, file: source}, nil
}

// add wires the sender to sink into g, and returns what rate control steers,
// nil for a file.
func (s *sender) add(g *pipeline.Graph, sink mrtp.Sink[mrtp.RTPPacket]) (mrtp.TargetBitrateSetter, error) {
	if s.file == nil {
		return sessionmedia.AddFakeSender(g, fakeConfig, sink)
	}
	return nil, sessionmedia.AddFileSender(g, s.file, sendMTU, sink)
}

// close releases a sender that was never added.
func (s *sender) close() error {
	if s.file == nil {
		return nil
	}
	return s.file.Close()
}

// frameSink is where a session's received frames go, and how many arrived.
type frameSink interface {
	mrtp.Sink[mrtp.EncodedFrame]
	Frames() uint64
}

// newSink returns the sink of track n, counted from 0, of session id: an IVF
// file in the sink directory, or a sink that drops the frames if there is
// none or the codec cannot be written to IVF.
func (m *media) newSink(id string, n int, codec mrtp.Codec) (frameSink, error) {
	if m.sinks == nil || (codec != mrtp.VP8 && codec != mrtp.VP9) {
		return discard{pipeline.NewDiscard[mrtp.EncodedFrame]()}, nil
	}
	name := id + ".ivf"
	if n > 0 {
		name = fmt.Sprintf("%v-%v.ivf", id, n+1)
	}
	file, err := m.sinks.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	return mediafile.NewIVFSink(file), nil
}

type discard struct {
	*pipeline.Discard[mrtp.EncodedFrame]
}

func (d discard) Frames() uint64 {
	return d.Packets()
}
