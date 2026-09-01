package quictransport

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"

	"github.com/quic-go/quic-go"
)

func OpenServerConn(ctx context.Context, localAddress string, quicConfig *quic.Config, tlsNextProtos []string) (*quic.Listener, *quic.Conn, error) {
	tlsConfig, err := generateTLSConfig("", "", nil, tlsNextProtos)
	if err != nil {
		return nil, nil, err
	}
	listener, err := quic.ListenAddr(localAddress, tlsConfig, quicConfig)
	if err != nil {
		return nil, nil, err
	}
	conn, err := listener.Accept(ctx)
	if err != nil {
		_ = listener.Close()
		return nil, nil, err
	}
	return listener, conn, nil
}

func OpenServerConnWithNet(ctx context.Context, quicConfig *quic.Config, tlsNextProtos []string, netConn net.PacketConn) (*quic.Transport, *quic.Listener, *quic.Conn, error) {
	tlsConfig, err := generateTLSConfig("", "", nil, tlsNextProtos)
	if err != nil {
		return nil, nil, nil, err
	}
	q := &quic.Transport{Conn: netConn}
	listener, err := q.Listen(tlsConfig, quicConfig)
	if err != nil {
		_ = q.Close()
		return nil, nil, nil, err
	}
	conn, err := listener.Accept(ctx)
	if err != nil {
		_ = listener.Close()
		_ = q.Close()
		return nil, nil, nil, err
	}
	return q, listener, conn, nil
}

func OpenClientConn(ctx context.Context, remoteAddress string, quicConfig *quic.Config, tlsNextProtos []string) (*quic.Conn, error) {
	conn, err := quic.DialAddr(ctx, remoteAddress, &tls.Config{
		InsecureSkipVerify: true,
		NextProtos:         tlsNextProtos,
	}, quicConfig)
	return conn, err
}

func OpenClientConnWithNet(ctx context.Context, remoteAddress string, quicConfig *quic.Config, tlsNextProtos []string, conn net.PacketConn) (*quic.Transport, *quic.Conn, error) {
	q := &quic.Transport{Conn: conn}

	var remoteAddr net.Addr
	if c, ok := conn.(net.Conn); ok {
		remoteAddr = c.RemoteAddr()
	} else {
		return nil, nil, fmt.Errorf("only implemented for net.Conn")
	}

	quicConn, err := q.Dial(ctx, remoteAddr, &tls.Config{
		InsecureSkipVerify: true,
		NextProtos:         tlsNextProtos,
	}, quicConfig)
	if err != nil {
		_ = q.Close()
		return nil, nil, err
	}
	return q, quicConn, nil
}
