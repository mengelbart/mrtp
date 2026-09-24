// Package udp implements plain RTP over UDP as a transport.
//
// Its flows are pipeline elements, one datagram per packet in both directions.
// The payload type is a parameter, so the same socket carries RTP or RTCP: the
// bytes function passed to a constructor says where that payload keeps its
// buffer.
package udp

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"sync"

	"github.com/mengelbart/mrtp"
	"github.com/mengelbart/mrtp/internal/logging"
	"github.com/mengelbart/mrtp/pipeline"
)

// datagramBufferSize is the size of the buffer one datagram is read into. It
// is the largest a datagram can be, so that a read cannot truncate a packet.
const datagramBufferSize = math.MaxUint16

// conn is the half of a UDP transport that does not depend on the direction.
// It deliberately has no Read or Write: a dialled socket cannot receive on a
// well known port and a listening socket cannot Write without a destination,
// so the send and receive elements expose one direction each.
type conn struct {
	socket *net.UDPConn
	logger *logging.RTPLogger

	closeOnce sync.Once
	closeErr  error
}

// resolve is a method rather than a constructor because conn holds a
// sync.Once, which must not be copied.
func (c *conn) resolve(address string, traceRTP bool, vantagePoint string) (*net.UDPAddr, error) {
	if traceRTP {
		c.logger = logging.NewRTPLogger(vantagePoint, nil)
	}
	return net.ResolveUDPAddr("udp", address)
}

func (c *conn) logRTP(packet []byte) {
	if c.logger != nil {
		c.logger.LogRTPPacketBuf(packet, nil)
	}
}

// Close implements mrtp.Element. It closes the socket, and is safe to call
// more than once.
func (c *conn) Close() error {
	c.closeOnce.Do(func() {
		c.closeErr = c.socket.Close()
	})
	return c.closeErr
}

// Sink sends packets to a remote UDP endpoint, one datagram per packet.
type Sink[T any] struct {
	conn
	bytes func(*T) *[]byte
}

// Dial connects to the UDP endpoint at address, in host:port form. Nothing is
// bound locally. bytes is where a payload keeps its buffer.
func Dial[T any](address string, traceRTP bool, bytes func(*T) *[]byte) (*Sink[T], error) {
	s := &Sink[T]{bytes: bytes}
	addr, err := s.resolve(address, traceRTP, "udp sink")
	if err != nil {
		return nil, err
	}
	if s.socket, err = net.DialUDP("udp", nil, addr); err != nil {
		return nil, err
	}
	return s, nil
}

// Negotiate implements mrtp.Sink. A socket takes any format, because it sends
// bytes and configures nothing.
func (s *Sink[T]) Negotiate(mrtp.Format) error {
	return nil
}

// Write implements mrtp.Sink, sending one datagram.
func (s *Sink[T]) Write(p mrtp.Packet[T]) error {
	defer p.Release()
	packet := *s.bytes(p.Value())
	s.logRTP(packet)
	_, err := s.socket.Write(packet)
	return err
}

// EndOfStream implements mrtp.Sink. A socket outlives the stream that ended,
// and is closed rather than ended.
func (s *Sink[T]) EndOfStream() error {
	return nil
}

// sourceOptions are the settings a receiving socket takes.
type sourceOptions struct {
	recvBufferSize int
}

// SourceOption configures a receiving socket.
type SourceOption func(*sourceOptions)

// ReceiveBufferSize sets the size of the socket's receive buffer in bytes. A
// size of zero leaves the operating system's default in place.
func ReceiveBufferSize(size int) SourceOption {
	return func(o *sourceOptions) {
		o.recvBufferSize = size
	}
}

// recvSocket is what the two receiving elements share: the bound socket, the
// pool their packets come from, and the one read that fills a packet.
type recvSocket[T any] struct {
	conn
	format mrtp.Format
	bytes  func(*T) *[]byte
	pool   *pipeline.Pool[T]
}

// listen binds the socket and builds the pool that feeds it.
func (s *recvSocket[T]) listen(address string, traceRTP bool, f mrtp.Format, bytes func(*T) *[]byte, opts []SourceOption) error {
	var settings sourceOptions
	for _, opt := range opts {
		opt(&settings)
	}

	s.format = f
	s.bytes = bytes
	s.pool = pipeline.NewPool(
		func() *T {
			var value T
			*bytes(&value) = make([]byte, datagramBufferSize)
			return &value
		},
		func(value *T) {
			buffer := bytes(value)
			*buffer = (*buffer)[:cap(*buffer)]
		},
	)

	addr, err := s.resolve(address, traceRTP, "udp source")
	if err != nil {
		return err
	}
	if s.socket, err = net.ListenUDP("udp", addr); err != nil {
		return err
	}
	if settings.recvBufferSize > 0 {
		if err = s.socket.SetReadBuffer(settings.recvBufferSize); err != nil {
			_ = s.socket.Close()
			return fmt.Errorf("failed to set receive buffer size: %w", err)
		}
	}
	return nil
}

// Format is what this socket's packets carry.
func (s *recvSocket[T]) Format() mrtp.Format {
	return s.format
}

// LocalAddr returns the address the socket is bound to.
func (s *recvSocket[T]) LocalAddr() *net.UDPAddr {
	return s.socket.LocalAddr().(*net.UDPAddr)
}

// read takes the next datagram as one owned packet.
func (s *recvSocket[T]) read() (mrtp.Packet[T], error) {
	packet := s.pool.Get()
	buffer := s.bytes(packet.Value())
	n, _, err := s.socket.ReadFrom(*buffer)
	if err != nil {
		packet.Release()
		return nil, err
	}
	*buffer = (*buffer)[:n]
	s.logRTP(*buffer)
	return packet, nil
}

// Source receives packets on a local UDP endpoint and pushes them downstream.
type Source[T any] struct {
	recvSocket[T]
	down mrtp.Sink[T]
}

// Listen binds the UDP endpoint at address, in host:port form, and pushes what
// arrives as packets of format f. bytes is where a payload keeps its buffer.
func Listen[T any](address string, traceRTP bool, f mrtp.Format, bytes func(*T) *[]byte, opts ...SourceOption) (*Source[T], error) {
	s := &Source[T]{}
	if err := s.listen(address, traceRTP, f, bytes, opts); err != nil {
		return nil, err
	}
	return s, nil
}

// Connect implements mrtp.Source.
func (s *Source[T]) Connect(down mrtp.Sink[T]) error {
	if s.down != nil {
		return errors.New("udp: source is already connected")
	}
	s.down = down
	return nil
}

// Run implements mrtp.Driver. It reads until the socket fails or is closed, or
// until ctx is cancelled.
func (s *Source[T]) Run(ctx context.Context) error {
	if s.down == nil {
		return errors.New("udp: source runs with its output wired")
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		packet, err := s.read()
		if err != nil {
			return err
		}
		if err := s.down.Write(packet); err != nil {
			return err
		}
	}
}

// Puller receives packets on a local UDP endpoint, one per Pull.
type Puller[T any] struct {
	recvSocket[T]
}

// ListenPuller binds the UDP endpoint at address, in host:port form, and hands
// out what arrives as packets of format f, one per Pull. bytes is where a
// payload keeps its buffer.
func ListenPuller[T any](address string, traceRTP bool, f mrtp.Format, bytes func(*T) *[]byte, opts ...SourceOption) (*Puller[T], error) {
	s := &Puller[T]{}
	if err := s.listen(address, traceRTP, f, bytes, opts); err != nil {
		return nil, err
	}
	return s, nil
}

// Pull implements mrtp.Puller. It ignores ctx once the read has started, so
// Close only unblocks it because closing the socket fails the read.
func (s *Puller[T]) Pull(ctx context.Context) (mrtp.Packet[T], error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	return s.read()
}

var (
	_ mrtp.Sink[mrtp.RTPPacket]    = (*Sink[mrtp.RTPPacket])(nil)
	_ mrtp.Source[mrtp.RTPPacket]  = (*Source[mrtp.RTPPacket])(nil)
	_ mrtp.Driver                  = (*Source[mrtp.RTPPacket])(nil)
	_ mrtp.Puller[mrtp.RTCPPacket] = (*Puller[mrtp.RTCPPacket])(nil)
)
