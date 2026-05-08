package webrtc

import (
	"fmt"
	"net"
	"time"

	"github.com/pion/rtp"
	"golang.org/x/sys/unix"
)

type udpConn struct {
	conn   *net.UDPConn
	setECN func(ssrc uint32, sequenceNumber uint16, ecn uint8)
}

func newUDPConn(conn *net.UDPConn, setECN func(ssrc uint32, sequenceNumber uint16, ecn uint8)) (*udpConn, error) {
	rawConn, err := conn.SyscallConn()
	if err != nil {
		return nil, fmt.Errorf("datastream: failed to get raw connection: %w", err)
	}

	var errECNIP error
	if err := rawConn.Control(func(fd uintptr) {
		errECNIP = unix.SetsockoptInt(int(fd), unix.IPPROTO_IP, unix.IP_RECVTOS, 1)
		errECNIP = unix.SetsockoptInt(int(fd), unix.IPPROTO_IPV6, unix.IPV6_RECVTCLASS, 1)

		_ = unix.SetsockoptInt(int(fd), unix.IPPROTO_IP, unix.IP_TOS, 0x02)
		_ = unix.SetsockoptInt(int(fd), unix.IPPROTO_IPV6, unix.IPV6_TCLASS, 0x02)
	}); err != nil {
		return nil, fmt.Errorf("datastream: failed to set socket options: %w", errECNIP)
	}

	return &udpConn{
		conn:   conn,
		setECN: setECN,
	}, nil
}

func (c *udpConn) Close() error {
	return c.conn.Close()
}

func (c *udpConn) LocalAddr() net.Addr {
	return c.conn.LocalAddr()
}

func (c *udpConn) RemoteAddr() net.Addr {
	return c.conn.RemoteAddr()
}

func (c *udpConn) SetDeadline(t time.Time) error {
	return c.conn.SetDeadline(t)
}

func (c *udpConn) SetReadDeadline(t time.Time) error {
	return c.conn.SetReadDeadline(t)
}

func (c *udpConn) SetWriteDeadline(t time.Time) error {
	return c.conn.SetWriteDeadline(t)
}

func (c *udpConn) SetReadBuffer(bytes int) error {
	return c.conn.SetReadBuffer(bytes)
}

func (c *udpConn) SetWriteBuffer(bytes int) error {
	return c.conn.SetWriteBuffer(bytes)
}

func (c *udpConn) Read(b []byte) (n int, err error) {
	return c.conn.Read(b)
}

func (c *udpConn) ReadFrom(p []byte) (n int, addr net.Addr, err error) {
	oobBuf := make([]byte, 1500)

	var oobn int

	n, oobn, _, addr, err = c.conn.ReadMsgUDP(p, oobBuf)
	if err != nil {
		return n, addr, err
	}
	if !matchSRTP(p) {
		return n, addr, nil
	}

	// check meta data for ecn
	packetOOB := make([]byte, oobn)
	copy(packetOOB, oobBuf[:oobn])

	msgs, err := unix.ParseSocketControlMessage(packetOOB[:oobn])
	if err != nil {
		return n, addr, err
	}

	// ECN reading
	var tos byte = 0
	for _, msg := range msgs {
		if msg.Header.Level == unix.IPPROTO_IP && msg.Header.Type == unix.IP_TOS {
			if len(msg.Data) >= 1 {
				tos = msg.Data[0]
				break
			}
		}
	}
	ecn := uint8(tos & 0x03)
	ssrc, sn, err := parseRTPHeader(p[:n])

	// store ecn value for this packet
	c.setECN(ssrc, sn, ecn)

	return n, addr, nil
}

func (c *udpConn) ReadFromUDP(b []byte) (n int, addr *net.UDPAddr, err error) {
	return c.conn.ReadFromUDP(b)
}

func (c *udpConn) ReadMsgUDP(b, oob []byte) (n, oobn, flags int, addr *net.UDPAddr, err error) {
	return c.conn.ReadMsgUDP(b, oob)
}

func (c *udpConn) Write(b []byte) (n int, err error) {
	return c.conn.Write(b)
}

func (c *udpConn) WriteTo(p []byte, addr net.Addr) (n int, err error) {
	return c.conn.WriteTo(p, addr)
}

func (c *udpConn) WriteToUDP(b []byte, addr *net.UDPAddr) (int, error) {
	return c.conn.WriteToUDP(b, addr)
}

func (c *udpConn) WriteMsgUDP(b, oob []byte, addr *net.UDPAddr) (n, oobn int, err error) {
	return c.conn.WriteMsgUDP(b, oob, addr)
}

func parseRTPHeader(b []byte) (uint32, uint16, error) {
	pkt := rtp.Packet{}
	if err := pkt.Unmarshal(b); err != nil {
		return 0, 0, err
	}
	return pkt.Header.SSRC, pkt.Header.SequenceNumber, nil
}
