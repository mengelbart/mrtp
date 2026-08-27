// Package udp implements plain RTP over UDP as a transport.
//
// A Sink and a Source are ordinary io.WriteCloser / io.ReadCloser with no
// further contract: they open their socket when they are created and close it
// when they are closed. A media pipeline that moves UDP itself is asked for a
// transport of its own instead, and this package is then not involved at all.
package udp

import (
	"fmt"
	"net"
	"sync"

	"github.com/mengelbart/mrtp/internal/logging"
)

// conn is the half of a UDP transport that does not depend on the direction.
// It deliberately has no Read or Write: a dialled socket cannot receive on a
// well known port and a listening socket cannot Write without a destination,
// so Sink and Source expose one direction each.
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

// Close closes the socket. It is safe to call more than once.
func (c *conn) Close() error {
	c.closeOnce.Do(func() {
		c.closeErr = c.socket.Close()
	})
	return c.closeErr
}

// Sink sends RTP packets to a remote UDP endpoint.
type Sink struct {
	conn
}

// Dial connects to the UDP endpoint at address, in host:port form. Nothing is
// bound locally.
func Dial(address string, traceRTP bool) (*Sink, error) {
	s := &Sink{}
	addr, err := s.resolve(address, traceRTP, "udp sink")
	if err != nil {
		return nil, err
	}
	if s.socket, err = net.DialUDP("udp", nil, addr); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Sink) Write(packet []byte) (int, error) {
	s.logRTP(packet)
	return s.socket.Write(packet)
}

// SourceOption configures a Source.
type SourceOption func(*Source)

// ReceiveBufferSize sets the size of the socket's receive buffer in bytes. A
// size of zero leaves the operating system's default in place.
func ReceiveBufferSize(size int) SourceOption {
	return func(s *Source) {
		s.recvBufferSize = size
	}
}

// Source receives RTP packets on a local UDP endpoint.
type Source struct {
	conn
	recvBufferSize int
}

// Listen binds the UDP endpoint at address, in host:port form.
func Listen(address string, traceRTP bool, opts ...SourceOption) (*Source, error) {
	s := &Source{}
	for _, opt := range opts {
		opt(s)
	}

	addr, err := s.resolve(address, traceRTP, "udp source")
	if err != nil {
		return nil, err
	}
	if s.socket, err = net.ListenUDP("udp", addr); err != nil {
		return nil, err
	}
	if s.recvBufferSize > 0 {
		if err = s.socket.SetReadBuffer(s.recvBufferSize); err != nil {
			_ = s.socket.Close()
			return nil, fmt.Errorf("failed to set receive buffer size: %w", err)
		}
	}
	return s, nil
}

// Read returns the payload of the next datagram. A datagram larger than buffer
// is truncated, like every other UDP reader in the tree.
func (s *Source) Read(buffer []byte) (int, error) {
	n, _, err := s.socket.ReadFrom(buffer)
	if err != nil {
		return n, err
	}
	s.logRTP(buffer[:n])
	return n, nil
}
