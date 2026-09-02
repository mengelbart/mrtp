package pipeline

import (
	"context"
	"errors"
	"io"

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
