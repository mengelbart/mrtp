package webrtc

import (
	"context"
	"fmt"
	"net"

	"github.com/pion/transport/v4"
	"github.com/wlynxg/anet"
)

const defaultRecvBufferSize = 10_000_000

type NetOption func(*Net) error

func SetRecvBufferSize(size int) NetOption {
	return func(n *Net) error {
		n.setRecvBufferSize = true
		n.recvBufferSize = size
		return nil
	}
}

type Net struct {
	interfaces []*transport.Interface

	setRecvBufferSize bool
	recvBufferSize    int
}

// CreateListenConfig implements [transport.Net].
func (n *Net) CreateListenConfig(c *net.ListenConfig) transport.ListenConfig {
	return stdListenConfig{c}
}

func NewNet(opts ...NetOption) (*Net, error) {
	n := &Net{
		interfaces:        []*transport.Interface{},
		setRecvBufferSize: false,
		recvBufferSize:    defaultRecvBufferSize,
	}
	for _, opt := range opts {
		if err := opt(n); err != nil {
			return nil, err
		}
	}

	return n, n.UpdateInterfaces()
}

func (n *Net) UpdateInterfaces() error {
	ifs := []*transport.Interface{}

	oifs, err := anet.Interfaces()
	if err != nil {
		return err
	}

	for i := range oifs {
		ifc := transport.NewInterface(oifs[i])

		addrs, err := anet.InterfaceAddrsByInterface(&oifs[i])
		if err != nil {
			return err
		}

		for _, addr := range addrs {
			ifc.AddAddress(addr)
		}

		ifs = append(ifs, ifc)
	}

	n.interfaces = ifs

	return nil
}

// CreateDialer implements transport.Net.
func (n *Net) CreateDialer(dialer *net.Dialer) transport.Dialer {
	return stdDialer{Dialer: dialer}
}

// Dial implements transport.Net.
func (n *Net) Dial(network string, address string) (net.Conn, error) {
	return net.Dial(network, address)
}

// DialTCP implements transport.Net.
func (n *Net) DialTCP(network string, laddr *net.TCPAddr, raddr *net.TCPAddr) (transport.TCPConn, error) {
	return net.DialTCP(network, laddr, raddr)
}

// DialUDP implements transport.Net.
func (n *Net) DialUDP(network string, laddr *net.UDPAddr, raddr *net.UDPAddr) (transport.UDPConn, error) {
	conn, err := net.DialUDP(network, laddr, raddr)
	if err != nil {
		return nil, err
	}
	if n.setRecvBufferSize {
		if err = conn.SetReadBuffer(n.recvBufferSize); err != nil {
			return nil, err
		}
	}
	return conn, nil
}

// InterfaceByIndex implements transport.Net.
func (n *Net) InterfaceByIndex(index int) (*transport.Interface, error) {
	for _, ifc := range n.interfaces {
		if ifc.Index == index {
			return ifc, nil
		}
	}
	return nil, fmt.Errorf("%w: index=%d", transport.ErrInterfaceNotFound, index)
}

// InterfaceByName implements transport.Net.
func (n *Net) InterfaceByName(name string) (*transport.Interface, error) {
	for _, ifc := range n.interfaces {
		if ifc.Name == name {
			return ifc, nil
		}
	}
	return nil, fmt.Errorf("%w: %s", transport.ErrInterfaceNotFound, name)
}

// Interfaces implements transport.Net.
func (n *Net) Interfaces() ([]*transport.Interface, error) {
	return n.interfaces, nil
}

// ListenPacket implements transport.Net.
func (n *Net) ListenPacket(network string, address string) (net.PacketConn, error) {
	return net.ListenPacket(network, address)
}

// ListenTCP implements transport.Net.
func (n *Net) ListenTCP(network string, laddr *net.TCPAddr) (transport.TCPListener, error) {
	l, err := net.ListenTCP(network, laddr)
	if err != nil {
		return nil, err
	}
	return tcpListener{l}, nil
}

// ListenUDP implements transport.Net.
func (n *Net) ListenUDP(network string, locAddr *net.UDPAddr) (transport.UDPConn, error) {
	conn, err := net.ListenUDP(network, locAddr)
	if err != nil {
		return nil, err
	}
	if n.setRecvBufferSize {
		if err = conn.SetReadBuffer(n.recvBufferSize); err != nil {
			return nil, err
		}
	}
	return conn, nil
}

// ResolveIPAddr implements transport.Net.
func (n *Net) ResolveIPAddr(network string, address string) (*net.IPAddr, error) {
	return net.ResolveIPAddr(network, address)
}

// ResolveTCPAddr implements transport.Net.
func (n *Net) ResolveTCPAddr(network string, address string) (*net.TCPAddr, error) {
	return net.ResolveTCPAddr(network, address)
}

// ResolveUDPAddr implements transport.Net.
func (n *Net) ResolveUDPAddr(network string, address string) (*net.UDPAddr, error) {
	return net.ResolveUDPAddr(network, address)
}

type tcpListener struct {
	*net.TCPListener
}

func (l tcpListener) AcceptTCP() (transport.TCPConn, error) {
	return l.TCPListener.AcceptTCP()
}

type stdDialer struct {
	*net.Dialer
}

func (d stdDialer) Dial(network, address string) (net.Conn, error) {
	return d.Dialer.Dial(network, address)
}

type stdListenConfig struct {
	*net.ListenConfig
}

func (d stdListenConfig) Listen(ctx context.Context, network, address string) (net.Listener, error) {
	return d.ListenConfig.Listen(ctx, network, address)
}

func (d stdListenConfig) ListenPacket(ctx context.Context, network, address string) (net.PacketConn, error) {
	return d.ListenConfig.ListenPacket(ctx, network, address)
}
