package pipeline

import (
	"context"
	"errors"
	"io"
	"sync"

	"github.com/mengelbart/mrtp"
)

// SinkFromWriter makes an io.WriteCloser the end of a graph, one packet per
// Write, and bytes is where a payload keeps its buffer. It takes any format,
// because a writer takes bytes and configures nothing.
func SinkFromWriter[T any](w io.WriteCloser, bytes func(*T) *[]byte) mrtp.Sink[T] {
	return &writerSink[T]{w: w, bytes: bytes}
}

type writerSink[T any] struct {
	w     io.WriteCloser
	bytes func(*T) *[]byte
}

func (s *writerSink[T]) Negotiate(mrtp.Format) error {
	return nil
}

func (s *writerSink[T]) Write(p mrtp.Packet[T]) error {
	defer p.Release()
	_, err := s.w.Write(*s.bytes(p.Value()))
	return err
}

func (s *writerSink[T]) EndOfStream() error {
	return nil
}

func (s *writerSink[T]) Close() error {
	return s.w.Close()
}

// SourceFromReader makes an io.ReadCloser the start of a graph, one packet per
// Read, carrying format f. Each packet is read into a buffer of size bytes,
// and bytes is where a payload keeps that buffer.
//
// The source is an [mrtp.Driver]: its Run reads until the reader ends, the
// reader fails, or ctx is cancelled.
func SourceFromReader[T any](r io.ReadCloser, f mrtp.Format, size int, bytes func(*T) *[]byte) mrtp.Source[T] {
	return &readerSource[T]{
		r:      r,
		format: f,
		bytes:  bytes,
		pool: NewPool(
			func() *T {
				var value T
				*bytes(&value) = make([]byte, size)
				return &value
			},
			func(value *T) {
				buffer := bytes(value)
				*buffer = (*buffer)[:cap(*buffer)]
			},
		),
	}
}

type readerSource[T any] struct {
	r      io.ReadCloser
	format mrtp.Format
	bytes  func(*T) *[]byte
	pool   *Pool[T]
	down   mrtp.Sink[T]
}

func (s *readerSource[T]) Format() mrtp.Format {
	return s.format
}

func (s *readerSource[T]) Connect(down mrtp.Sink[T]) error {
	if s.down != nil {
		return errors.New("pipeline: reader source is already connected")
	}
	s.down = down
	return nil
}

func (s *readerSource[T]) Run(ctx context.Context) error {
	if s.down == nil {
		return errors.New("pipeline: reader source runs with its output wired")
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		packet := s.pool.Get()
		buffer := s.bytes(packet.Value())
		n, err := s.r.Read(*buffer)
		if err != nil {
			packet.Release()
			if errors.Is(err, io.EOF) {
				return s.down.EndOfStream()
			}
			return err
		}
		*buffer = (*buffer)[:n]
		if err := s.down.Write(packet); err != nil {
			return err
		}
	}
}

func (s *readerSource[T]) Close() error {
	return s.r.Close()
}

var (
	_ mrtp.Sink[mrtp.DataChunk]   = (*writerSink[mrtp.DataChunk])(nil)
	_ mrtp.Source[mrtp.DataChunk] = (*readerSource[mrtp.DataChunk])(nil)
	_ mrtp.Driver                 = (*readerSource[mrtp.DataChunk])(nil)
)

// WriterFromSink makes an [mrtp.Sink] the end of an io.WriteCloser, one packet
// per Write, and bytes is where a payload keeps its buffer. It negotiates
// format f once, at construction.
//
// It is the inverse of [SinkFromWriter].
func WriterFromSink[T any](down mrtp.Sink[T], f mrtp.Format, bytes func(*T) *[]byte) (io.WriteCloser, error) {
	if err := down.Negotiate(f); err != nil {
		return nil, err
	}
	return &sinkWriter[T]{
		down:  down,
		bytes: bytes,
		pool: NewPool(
			func() *T {
				var value T
				return &value
			},
			func(value *T) {
				buffer := bytes(value)
				*buffer = (*buffer)[:0]
			},
		),
	}, nil
}

type sinkWriter[T any] struct {
	down   mrtp.Sink[T]
	bytes  func(*T) *[]byte
	pool   *Pool[T]
	closed sync.Once
}

func (w *sinkWriter[T]) Write(p []byte) (int, error) {
	packet := w.pool.Get()
	buffer := w.bytes(packet.Value())
	*buffer = append((*buffer)[:0], p...)
	if err := w.down.Write(packet); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (w *sinkWriter[T]) Close() error {
	var err error
	w.closed.Do(func() {
		err = errors.Join(w.down.EndOfStream(), w.down.Close())
	})
	return err
}

// SinkReader is a pushing input read out as a byte stream.
type SinkReader[T any] interface {
	mrtp.Sink[T]
	io.ReadCloser
}

// ReaderFromSource makes a pushing upstream the start of an io.ReadCloser, one
// packet per Read, and bytes is where a payload keeps its buffer. It takes any
// format, because a reader takes bytes and configures nothing.
//
// It buffers nothing: a packet is handed straight to the next Read, so the
// upstream blocks in Write until the reader takes it. That is what keeps a
// pulling consumer's demand as the back pressure on the source.
//
// It is the inverse of [SourceFromReader].
func ReaderFromSource[T any](bytes func(*T) *[]byte) SinkReader[T] {
	return &sourceReader[T]{
		bytes:  bytes,
		items:  make(chan mrtp.Packet[T]),
		eos:    make(chan struct{}),
		closed: make(chan struct{}),
	}
}

type sourceReader[T any] struct {
	bytes func(*T) *[]byte

	items chan mrtp.Packet[T]
	eos   chan struct{}

	closed  chan struct{}
	closing sync.Once
}

func (r *sourceReader[T]) Negotiate(mrtp.Format) error {
	return nil
}

// Write implements mrtp.Sink. It blocks until a Read takes the packet, or
// until the reader is closed, which releases the packet.
func (r *sourceReader[T]) Write(p mrtp.Packet[T]) error {
	select {
	case r.items <- p:
		return nil
	case <-r.closed:
		p.Release()
		return io.ErrClosedPipe
	}
}

// EndOfStream implements mrtp.Sink. Read reports io.EOF from here on.
func (r *sourceReader[T]) EndOfStream() error {
	close(r.eos)
	return nil
}

// Read implements io.Reader. It returns one packet per call, and
// io.ErrShortBuffer rather than a truncated packet if p is too small.
func (r *sourceReader[T]) Read(p []byte) (int, error) {
	select {
	case packet := <-r.items:
		defer packet.Release()
		buffer := *r.bytes(packet.Value())
		if len(p) < len(buffer) {
			return 0, io.ErrShortBuffer
		}
		return copy(p, buffer), nil
	case <-r.eos:
		return 0, io.EOF
	case <-r.closed:
		return 0, io.ErrClosedPipe
	}
}

// Close implements mrtp.Element and io.Closer. It unblocks a pending Write and
// a pending Read, and may be called more than once.
func (r *sourceReader[T]) Close() error {
	r.closing.Do(func() { close(r.closed) })
	return nil
}

var (
	_ io.WriteCloser             = (*sinkWriter[mrtp.DataChunk])(nil)
	_ SinkReader[mrtp.DataChunk] = (*sourceReader[mrtp.DataChunk])(nil)
)
