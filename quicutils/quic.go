package quicutils

import (
	"context"
	"crypto/tls"

	"github.com/quic-go/quic-go"
)

func OpenServerConn(localAddress string, quicConfig *quic.Config, tlsNextProtos []string) (*quic.Conn, error) {
	tlsConfig, err := GenerateTLSConfig("", "", nil, tlsNextProtos)
	if err != nil {
		return nil, err
	}
	listener, err := quic.ListenAddr(localAddress, tlsConfig, quicConfig)
	if err != nil {
		return nil, err
	}
	conn, err := listener.Accept(context.TODO())
	if err != nil {
		return nil, err
	}

	return conn, nil
}

func OpenClientConn(remoteAddress string, quicConfig *quic.Config, tlsNextProtos []string) (*quic.Conn, error) {
	return quic.DialAddr(context.TODO(), remoteAddress, &tls.Config{
		InsecureSkipVerify: true,
		NextProtos:         tlsNextProtos,
	}, quicConfig)
}
