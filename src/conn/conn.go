package conn

import (
	"errors"
	"github.com/esrrhs/go-engine/src/common"
	"io"
	"strings"
)

type Conn interface {
	io.ReadWriteCloser

	Name() string

	Info() string

	Dial(dst string) (Conn, error)

	Listen(dst string) (Conn, error)
	Accept() (Conn, error)
}

func NewConn(proto string) (Conn, error) {
	proto = strings.ToLower(proto)
	if proto == "tcp" {
		return &tcpConn{}, nil
	} else if proto == "udp" {
		return &udpConn{}, nil
	} else if proto == "rudp" {
		return &rudpConn{}, nil
	} else if proto == "ricmp" {
		return &ricmpConn{id: common.UniqueId()}, nil
	}
	return nil, errors.New("undefined proto " + proto)
}
