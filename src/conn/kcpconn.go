package conn

import (
	"errors"
	"github.com/xtaci/kcp-go"
	"github.com/xtaci/smux"
)

type kcpConn struct {
	session  *smux.Session
	stream   *smux.Stream
	listener *kcp.Listener
	info     string
}

func (c *kcpConn) Name() string {
	return "kcp"
}

func (c *kcpConn) Read(p []byte) (n int, err error) {
	if c.stream != nil {
		return c.stream.Read(p)
	}
	return 0, errors.New("empty conn")
}

func (c *kcpConn) Write(p []byte) (n int, err error) {
	if c.stream != nil {
		return c.stream.Write(p)
	}
	return 0, errors.New("empty conn")
}

func (c *kcpConn) Close() error {
	if c.session != nil {
		return c.session.Close()
	} else if c.listener != nil {
		return c.listener.Close()
	}
	return nil
}

func (c *kcpConn) Info() string {
	if c.info != "" {
		return c.info
	}
	if c.session != nil {
		c.info = c.session.LocalAddr().String() + "<--kcp-->" + c.session.RemoteAddr().String()
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

	c.setParam(conn.(*kcp.UDPSession))

	session, err := smux.Client(conn, nil)
	if err != nil {
		return nil, err
	}

	stream, err := session.OpenStream()
	if err != nil {
		return nil, err
	}

	return &kcpConn{session: session, stream: stream}, nil
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

	session, err := smux.Server(conn, nil)
	if err != nil {
		return nil, err
	}

	stream, err := session.AcceptStream()
	if err != nil {
		return nil, err
	}

	return &kcpConn{session: session, stream: stream}, nil
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
