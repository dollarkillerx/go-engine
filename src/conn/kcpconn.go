package conn

import (
	"errors"
	"github.com/xtaci/kcp-go"
	"time"
)

type kcpConn struct {
	conn     *kcp.UDPSession
	listener *kcp.Listener
	info     string
}

func (c *kcpConn) Name() string {
	return "kcp"
}

func (c *kcpConn) Read(p []byte) (n int, err error) {
	if c.conn != nil {
		return c.conn.Read(p)
	}
	return 0, errors.New("empty conn")
}

func (c *kcpConn) Write(p []byte) (n int, err error) {
	if c.conn != nil {
		return c.conn.Write(p)
	}
	return 0, errors.New("empty conn")
}

func (c *kcpConn) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	} else if c.listener != nil {
		return c.listener.Close()
	}
	return nil
}

func (c *kcpConn) Info() string {
	if c.info != "" {
		return c.info
	}
	if c.conn != nil {
		c.info = c.conn.LocalAddr().String() + "<--kcp-->" + c.conn.RemoteAddr().String()
	} else if c.listener != nil {
		c.info = "kcp--" + c.listener.Addr().String()
	} else {
		c.info = "empty kcp conn"
	}
	return c.info
}

func (c *kcpConn) Dial(dst string) (Conn, error) {
	conn, err := kcp.Dial(dst)
	if err != nil {
		return nil, err
	}

	// kcp client如果不发包，server无法accept
	last := time.Now()
	for {
		conn.(*kcp.UDPSession).Write([]byte("hello"))
		time.Sleep(time.Millisecond)
		if time.Now().Sub(last) > time.Second {
			break
		}
	}

	c.setParam(conn.(*kcp.UDPSession))

	return &kcpConn{conn: conn.(*kcp.UDPSession)}, nil
}

func (c *kcpConn) Listen(dst string) (Conn, error) {
	listener, err := kcp.Listen(dst)
	if err != nil {
		return nil, err
	}

	listener.(*kcp.Listener).SetReadBuffer(4 * 1024 * 1024)
	listener.(*kcp.Listener).SetWriteBuffer(4 * 1024 * 1024)
	listener.(*kcp.Listener).SetDSCP(46)

	return &kcpConn{listener: listener.(*kcp.Listener)}, nil
}

func (c *kcpConn) Accept() (Conn, error) {
	conn, err := c.listener.Accept()
	if err != nil {
		return nil, err
	}

	c.setParam(conn.(*kcp.UDPSession))

	return &kcpConn{conn: conn.(*kcp.UDPSession)}, nil
}

func (c *kcpConn) setParam(conn *kcp.UDPSession) {
	conn.SetStreamMode(true)
	conn.SetWindowSize(10000, 10000)
	conn.SetReadBuffer(16 * 1024 * 1024)
	conn.SetWriteBuffer(16 * 1024 * 1024)
	conn.SetNoDelay(0, 100, 1, 1)
	conn.SetMtu(500)
	conn.SetACKNoDelay(false)
}
