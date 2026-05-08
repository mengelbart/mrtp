package webrtc

import (
	"fmt"
	"net"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

type UDPConn struct {
	conn *net.UDPConn

	curEcn uint8
	ecnMap *sync.Map
}

func NewUDPConn(conn *net.UDPConn, ecnMap *sync.Map) (*UDPConn, error) {
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

	return &UDPConn{
		conn:   conn,
		ecnMap: ecnMap,
	}, nil
}

func (c *UDPConn) Close() error {
	return c.conn.Close()
}

func (c *UDPConn) LocalAddr() net.Addr {
	return c.conn.LocalAddr()
}

func (c *UDPConn) RemoteAddr() net.Addr {
	return c.conn.RemoteAddr()
}

func (c *UDPConn) SetDeadline(t time.Time) error {
	return c.conn.SetDeadline(t)
}

func (c *UDPConn) SetReadDeadline(t time.Time) error {
	return c.conn.SetReadDeadline(t)
}

func (c *UDPConn) SetWriteDeadline(t time.Time) error {
	return c.conn.SetWriteDeadline(t)
}

func (c *UDPConn) SetReadBuffer(bytes int) error {
	return c.conn.SetReadBuffer(bytes)
}

func (c *UDPConn) SetWriteBuffer(bytes int) error {
	return c.conn.SetWriteBuffer(bytes)
}

func (c *UDPConn) Read(b []byte) (n int, err error) {
	fmt.Println("Read")
	return c.conn.Read(b)
}

func (c *UDPConn) ReadFrom(p []byte) (n int, addr net.Addr, err error) {
	fmt.Println("ReadFrom")

	oobBuf := make([]byte, 1500)

	var oobn int

	n, oobn, _, addr, err = c.conn.ReadMsgUDP(p, oobBuf)
	if err != nil {
		return n, addr, err
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
	if ecn != c.curEcn {
		fmt.Printf("ECN bits of incoming packages changed from %02b to %02b\n", int(c.curEcn), ecn)
	}
	c.curEcn = ecn

	// TODO: we also parse non rtp packets here
	rtpID, err := GetRTPidFromPacket(p)
	if err != nil {
		fmt.Println("Could not parse")
	}

	fmt.Printf("ECN %02b for: %d, SequenceNumber: %d\n", ecn, rtpID.SSRC, rtpID.SequenceNumber)

	// store ecn value for this packet
	c.ecnMap.Store(rtpID, ecn)

	return n, addr, nil
}

func (c *UDPConn) ReadFromUDP(b []byte) (n int, addr *net.UDPAddr, err error) {
	fmt.Println("ReadFromUDP")
	return c.conn.ReadFromUDP(b)
}

func (c *UDPConn) ReadMsgUDP(b, oob []byte) (n, oobn, flags int, addr *net.UDPAddr, err error) {
	fmt.Println("ReadMsgUDP")
	return c.conn.ReadMsgUDP(b, oob)
}

func (c *UDPConn) Write(b []byte) (n int, err error) {
	return c.conn.Write(b)
}

func (c *UDPConn) WriteTo(p []byte, addr net.Addr) (n int, err error) {
	return c.conn.WriteTo(p, addr)
}

func (c *UDPConn) WriteToUDP(b []byte, addr *net.UDPAddr) (int, error) {
	return c.conn.WriteToUDP(b, addr)
}

func (c *UDPConn) WriteMsgUDP(b, oob []byte, addr *net.UDPAddr) (n, oobn int, err error) {
	return c.conn.WriteMsgUDP(b, oob, addr)
}
