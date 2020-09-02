package conn

import (
	"context"
	"errors"
	"github.com/esrrhs/go-engine/src/common"
	"github.com/esrrhs/go-engine/src/frame"
	"github.com/esrrhs/go-engine/src/group"
	"github.com/esrrhs/go-engine/src/loggo"
	"github.com/golang/protobuf/proto"
	"net"
	"sync"
	"time"
)

type RudpConfig struct {
	MaxPacketSize      int
	CutSize            int
	MaxId              int
	BufferSize         int
	MaxWin             int
	ResendTimems       int
	Compress           int
	Stat               int
	ConnectTimeoutMs   int
	CloseTimeoutMs     int
	CloseWaitTimeoutMs int
	AcceptChanLen      int
}

func DefaultRudpConfig() *RudpConfig {
	return &RudpConfig{
		MaxPacketSize:      1024,
		CutSize:            500,
		MaxId:              10000,
		BufferSize:         1024 * 1024,
		MaxWin:             1000,
		ResendTimems:       200,
		Compress:           0,
		Stat:               0,
		ConnectTimeoutMs:   10000,
		CloseTimeoutMs:     5000,
		CloseWaitTimeoutMs: 5000,
		AcceptChanLen:      128,
	}
}

type rudpConn struct {
	info          string
	config        *RudpConfig
	dialer        *rudpConnDialer
	listenersonny *rudpConnListenerSonny
	listener      *rudpConnListener
	cancel        context.CancelFunc
	isclose       bool
	closelock     sync.Mutex
}

type rudpConnDialer struct {
	conn *net.UDPConn
	fm   *frame.FrameMgr
	wg   *group.Group
}

type rudpConnListenerSonny struct {
	dstaddr    *net.UDPAddr
	fatherconn *net.UDPConn
	fm         *frame.FrameMgr
	wg         *group.Group
}

type rudpConnListener struct {
	listenerconn *net.UDPConn
	wg           *group.Group
	sonny        sync.Map
	accept       *common.Channel
}

func (c *rudpConn) Name() string {
	return "rudp"
}

func (c *rudpConn) Read(p []byte) (n int, err error) {
	c.checkConfig()

	if c.isclose {
		return 0, errors.New("read closed conn")
	}

	if len(p) <= 0 {
		return 0, errors.New("read empty buffer")
	}

	var fm *frame.FrameMgr
	var wg *group.Group
	if c.dialer != nil {
		fm = c.dialer.fm
		wg = c.dialer.wg
	} else if c.listener != nil {
		return 0, errors.New("listener can not be read")
	} else if c.listenersonny != nil {
		fm = c.listenersonny.fm
		wg = c.listenersonny.wg
	} else {
		return 0, errors.New("empty conn")
	}

	for !c.isclose {
		if fm.GetRecvBufferSize() <= 0 {
			if wg != nil && wg.IsExit() {
				return 0, errors.New("closed conn")
			}
			time.Sleep(time.Millisecond * 100)
			continue
		}

		size := copy(p, fm.GetRecvReadLineBuffer())
		fm.SkipRecvBuffer(size)
		return size, nil
	}

	return 0, errors.New("read closed conn")
}

func (c *rudpConn) Write(p []byte) (n int, err error) {
	c.checkConfig()

	if c.isclose {
		return 0, errors.New("write closed conn")
	}

	if len(p) <= 0 {
		return 0, errors.New("write empty data")
	}

	var fm *frame.FrameMgr
	var wg *group.Group
	if c.dialer != nil {
		fm = c.dialer.fm
		wg = c.dialer.wg
	} else if c.listener != nil {
		return 0, errors.New("listener can not be write")
	} else if c.listenersonny != nil {
		fm = c.listenersonny.fm
		wg = c.listenersonny.wg
	} else {
		return 0, errors.New("empty conn")
	}

	totalsize := len(p)
	cur := 0

	for !c.isclose {
		size := totalsize - cur
		svleft := fm.GetSendBufferLeft()
		if size > svleft {
			size = svleft
		}

		if size <= 0 {
			if wg != nil && wg.IsExit() {
				return 0, errors.New("closed conn")
			}
			time.Sleep(time.Millisecond * 100)
			continue
		}

		fm.WriteSendBuffer(p[cur : cur+size])
		cur += size

		if cur >= totalsize {
			return totalsize, nil
		}

		time.Sleep(time.Millisecond * 100)
	}

	return 0, errors.New("write closed conn")
}

func (c *rudpConn) Close() error {
	c.checkConfig()

	if c.isclose {
		return nil
	}

	c.closelock.Lock()
	defer c.closelock.Unlock()

	loggo.Debug("start Close %s", c.Info())

	if c.cancel != nil {
		c.cancel()
	}
	if c.dialer != nil {
		if c.dialer.wg != nil {
			loggo.Debug("start Close dialer %s", c.Info())
			c.dialer.wg.Stop()
			c.dialer.wg.Wait()
		}
		if c.dialer.conn != nil {
			c.dialer.conn.Close()
		}
	} else if c.listener != nil {
		if c.listener.wg != nil {
			loggo.Debug("start Close listener %s", c.Info())
			c.listener.wg.Stop()
			c.listener.sonny.Range(func(key, value interface{}) bool {
				u := value.(*rudpConn)
				u.Close()
				return true
			})
			c.listener.wg.Wait()
		}
		if c.listener.listenerconn != nil {
			c.listener.listenerconn.Close()
		}
	} else if c.listenersonny != nil {
		if c.listenersonny.wg != nil {
			loggo.Debug("start Close listenersonny %s", c.Info())
			c.listenersonny.wg.Stop()
			c.listenersonny.wg.Wait()
		}
	}
	c.isclose = true

	loggo.Debug("Close ok %s", c.Info())

	return nil
}

func (c *rudpConn) Info() string {
	c.checkConfig()

	if c.info != "" {
		return c.info
	}
	if c.dialer != nil {
		c.info = c.dialer.conn.LocalAddr().String() + "<--rudp-->" + c.dialer.conn.RemoteAddr().String()
	} else if c.listener != nil {
		c.info = "rudp--" + c.listener.listenerconn.LocalAddr().String()
	} else if c.listenersonny != nil {
		c.info = c.listenersonny.fatherconn.LocalAddr().String() + "<--rudp-->" + c.listenersonny.dstaddr.String()
	} else {
		c.info = "empty rudp conn"
	}
	return c.info
}

func (c *rudpConn) Dial(dst string) (Conn, error) {
	c.checkConfig()

	addr, err := net.ResolveUDPAddr("udp", dst)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	var d net.Dialer
	conn, err := d.DialContext(ctx, "udp", addr.String())
	if err != nil {
		return nil, err
	}
	c.cancel = nil

	id := common.Guid()
	fm := frame.NewFrameMgr(c.config.CutSize, c.config.MaxId, c.config.BufferSize, c.config.MaxWin, c.config.ResendTimems, c.config.Compress, c.config.Stat)
	fm.SetDebugid(id)

	dialer := &rudpConnDialer{conn: conn.(*net.UDPConn), fm: fm}

	u := &rudpConn{config: c.config, dialer: dialer}

	loggo.Debug("start connect remote rudp %s %s", u.Info(), id)

	u.dialer.fm.Connect()

	startConnectTime := time.Now()
	buf := make([]byte, c.config.MaxPacketSize)
	for {
		if u.dialer.fm.IsConnected() {
			break
		}

		u.dialer.fm.Update()

		// send udp
		sendlist := u.dialer.fm.GetSendList()
		for e := sendlist.Front(); e != nil; e = e.Next() {
			f := e.Value.(*frame.Frame)
			mb, _ := u.dialer.fm.MarshalFrame(f)
			u.dialer.conn.SetReadDeadline(time.Now().Add(time.Millisecond * 100))
			u.dialer.conn.Write(mb)
		}

		// recv udp
		u.dialer.conn.SetReadDeadline(time.Now().Add(time.Millisecond * 100))
		n, _ := u.dialer.conn.Read(buf)
		if n > 0 {
			f := &frame.Frame{}
			err := proto.Unmarshal(buf[0:n], f)
			if err == nil {
				u.dialer.fm.OnRecvFrame(f)
			} else {
				loggo.Error("%s %s Unmarshal fail %s", c.Info(), u.Info(), err)
				break
			}
		}

		if c.isclose {
			loggo.Debug("can not connect remote rudp %s", u.Info())
			break
		}

		// timeout
		now := time.Now()
		diffclose := now.Sub(startConnectTime)
		if diffclose > time.Millisecond*time.Duration(c.config.ConnectTimeoutMs) {
			loggo.Debug("can not connect remote rudp %s", u.Info())
			break
		}

		time.Sleep(time.Millisecond * 10)
	}

	if c.isclose {
		u.Close()
		return nil, errors.New("closed conn")
	}

	if u.isclose {
		return nil, errors.New("closed conn")
	}

	if !u.dialer.fm.IsConnected() {
		return nil, errors.New("connect timeout")
	}

	loggo.Debug("connect remote ok rudp %s", u.Info())

	wg := group.NewGroup("rudpConn serveListenerSonny"+" "+u.Info(), nil, nil)

	u.dialer.wg = wg

	wg.Go("rudpConn updateDialerSonny"+" "+u.Info(), func() error {
		return u.updateDialerSonny()
	})

	return u, nil
}

func (c *rudpConn) Listen(dst string) (Conn, error) {
	c.checkConfig()

	ipaddr, err := net.ResolveUDPAddr("udp", dst)
	if err != nil {
		return nil, err
	}

	listenerconn, err := net.ListenUDP("udp", ipaddr)
	if err != nil {
		return nil, err
	}

	ch := common.NewChannel(c.config.AcceptChanLen)

	wg := group.NewGroup("rudpConn Listen"+" "+dst, nil, nil)

	listener := &rudpConnListener{
		listenerconn: listenerconn,
		wg:           wg,
		accept:       ch,
	}

	u := &rudpConn{config: c.config, listener: listener}
	wg.Go("rudpConn loopListenerRecv"+" "+dst, func() error {
		return u.loopListenerRecv()
	})

	return u, nil
}

func (c *rudpConn) Accept() (Conn, error) {
	c.checkConfig()

	if c.listener.wg == nil {
		return nil, errors.New("not listen")
	}
	for !c.listener.wg.IsExit() {
		s := <-c.listener.accept.Ch()
		if s == nil {
			break
		}
		sonny := s.(*rudpConn)
		_, ok := c.listener.sonny.Load(sonny.listenersonny.dstaddr.String())
		if !ok {
			continue
		}
		if sonny.isclose {
			continue
		}
		return sonny, nil
	}
	return nil, errors.New("listener close")
}

func (c *rudpConn) checkConfig() {
	if c.config == nil {
		c.config = DefaultRudpConfig()
	}
}

func (c *rudpConn) SetConfig(config *RudpConfig) {
	c.config = config
}

func (c *rudpConn) loopListenerRecv() error {
	c.checkConfig()

	buf := make([]byte, c.config.MaxPacketSize)
	for !c.listener.wg.IsExit() {
		c.listener.listenerconn.SetReadDeadline(time.Now().Add(time.Millisecond * 100))
		n, srcaddr, err := c.listener.listenerconn.ReadFromUDP(buf)
		if err != nil {
			continue
		}

		srcaddrstr := srcaddr.String()

		v, ok := c.listener.sonny.Load(srcaddrstr)
		if !ok {
			id := common.Guid()
			fm := frame.NewFrameMgr(c.config.CutSize, c.config.MaxId, c.config.BufferSize, c.config.MaxWin, c.config.ResendTimems, c.config.Compress, c.config.Stat)
			fm.SetDebugid(id)

			sonny := &rudpConnListenerSonny{
				dstaddr:    srcaddr,
				fatherconn: c.listener.listenerconn,
				fm:         fm,
			}

			u := &rudpConn{config: c.config, listenersonny: sonny}
			c.listener.sonny.Store(srcaddrstr, u)

			c.listener.wg.Go("rudpConn accept"+" "+u.Info(), func() error {
				return c.accept(u)
			})

			loggo.Debug("start accept remote rudp %s %s", u.Info(), id)
		} else {
			u := v.(*rudpConn)

			f := &frame.Frame{}
			err := proto.Unmarshal(buf[0:n], f)
			if err == nil {
				u.listenersonny.fm.OnRecvFrame(f)
				//loggo.Debug("%s recv frame %d", u.Info(), f.Id)
			} else {
				loggo.Error("%s %s Unmarshal fail %s", c.Info(), u.Info(), err)
			}
		}

		c.listener.sonny.Range(func(key, value interface{}) bool {
			u := value.(*rudpConn)
			if u.isclose {
				c.listener.sonny.Delete(key)
				loggo.Debug("delete sonny from map %s", u.Info())
			}
			return true
		})
	}
	return nil
}

func (c *rudpConn) accept(u *rudpConn) error {

	loggo.Debug("server begin accept rudp %s", u.Info())

	startConnectTime := time.Now()
	done := false
	for !c.listener.wg.IsExit() {

		if u.listenersonny.fm.IsConnected() {
			done = true
			break
		}

		u.listenersonny.fm.Update()

		// send udp
		sendlist := u.listenersonny.fm.GetSendList()
		for e := sendlist.Front(); e != nil; e = e.Next() {
			f := e.Value.(*frame.Frame)
			mb, err := u.listenersonny.fm.MarshalFrame(f)
			if err != nil {
				loggo.Error("MarshalFrame fail %s", err)
				break
			}
			u.listenersonny.fatherconn.SetWriteDeadline(time.Now().Add(time.Millisecond * 100))
			u.listenersonny.fatherconn.WriteToUDP(mb, u.listenersonny.dstaddr)
		}

		now := time.Now()
		diffclose := now.Sub(startConnectTime)
		if diffclose > time.Millisecond*time.Duration(c.config.ConnectTimeoutMs) {
			loggo.Debug("can not connect by remote rudp %s", u.Info())
			break
		}

		time.Sleep(time.Millisecond * 10)
	}

	if !done {
		u.Close()
		return nil
	}

	if c.listener.wg.IsExit() {
		u.Close()
		return nil
	}

	loggo.Debug("server accept rudp ok %s", u.Info())

	c.listener.accept.Write(u)

	wg := group.NewGroup("rudpConn ListenerSonny"+" "+u.Info(), c.listener.wg, nil)

	u.listenersonny.wg = wg

	wg.Go("rudpConn updateListenerSonny"+" "+u.Info(), func() error {
		return u.updateListenerSonny()
	})

	loggo.Debug("accept rudp finish %s", u.Info())

	return nil
}

func (c *rudpConn) updateListenerSonny() error {
	return c.update_rudp(c.listenersonny.wg, c.listenersonny.fm, c.listenersonny.fatherconn, c.listenersonny.dstaddr, false)
}

func (c *rudpConn) updateDialerSonny() error {
	return c.update_rudp(c.dialer.wg, c.dialer.fm, c.dialer.conn, nil, true)
}

func (c *rudpConn) update_rudp(wg *group.Group, fm *frame.FrameMgr, conn *net.UDPConn, dstaddr *net.UDPAddr, readconn bool) error {

	loggo.Debug("start rudp conn %s", c.Info())

	stage := "open"

	if readconn {
		wg.Go("rudpConn update_rudp recv"+" "+c.Info(), func() error {
			bytes := make([]byte, c.config.MaxPacketSize)
			for !wg.IsExit() && stage != "closewait" {
				// recv udp
				conn.SetReadDeadline(time.Now().Add(time.Millisecond * 100))
				n, _ := conn.Read(bytes)
				if n > 0 {
					f := &frame.Frame{}
					err := proto.Unmarshal(bytes[0:n], f)
					if err == nil {
						fm.OnRecvFrame(f)
						//loggo.Debug("%s recv frame %d", c.Info(), f.Id)
					} else {
						loggo.Error("Unmarshal fail from %s %s", c.Info(), err)
					}
				}
			}

			return nil
		})
	}

	reason := ""

	for !wg.IsExit() {

		avctive := fm.Update()

		// send udp
		sendlist := fm.GetSendList()
		for e := sendlist.Front(); e != nil; e = e.Next() {
			f := e.Value.(*frame.Frame)
			mb, err := fm.MarshalFrame(f)
			if err != nil {
				loggo.Error("MarshalFrame fail %s", err)
				return err
			}
			conn.SetWriteDeadline(time.Now().Add(time.Millisecond * 100))
			if dstaddr != nil {
				conn.WriteToUDP(mb, dstaddr)
				//loggo.Debug("%s send frame to %s %d", c.Info(), dstaddr, f.Id)
			} else {
				conn.Write(mb)
				//loggo.Debug("%s send frame %d", c.Info(), f.Id)
			}
		}

		// timeout
		if fm.IsHBTimeout() {
			reason = "HBTimeout"
			loggo.Debug("close inactive conn %s", c.Info())
			break
		}

		if fm.IsRemoteClosed() {
			reason = "RemoteClose"
			loggo.Debug("closed by remote conn %s", c.Info())
			break
		}

		if !avctive && sendlist.Len() <= 0 {
			time.Sleep(time.Millisecond * 10)
		}
	}

	stage = "close"
	fm.Close()
	loggo.Debug("close rudp conn fm %s", c.Info())

	startCloseTime := time.Now()
	for !wg.IsExit() {
		now := time.Now()

		fm.Update()

		// send udp
		sendlist := fm.GetSendList()
		for e := sendlist.Front(); e != nil; e = e.Next() {
			f := e.Value.(*frame.Frame)
			mb, err := fm.MarshalFrame(f)
			if err != nil {
				loggo.Error("MarshalFrame fail %s", err)
				return err
			}
			conn.SetWriteDeadline(time.Now().Add(time.Millisecond * 100))
			if dstaddr != nil {
				conn.WriteToUDP(mb, dstaddr)
				//loggo.Debug("%s send frame to %s %d", c.Info(), dstaddr, f.Id)
			} else {
				conn.Write(mb)
				//loggo.Debug("%s send frame %d", c.Info(), f.Id)
			}
		}

		diffclose := now.Sub(startCloseTime)
		if diffclose > time.Millisecond*time.Duration(c.config.CloseTimeoutMs) {
			loggo.Debug("close conn had timeout %s", c.Info())
			break
		}

		remoteclosed := fm.IsRemoteClosed()
		if remoteclosed {
			loggo.Debug("remote conn had closed %s", c.Info())
			break
		}

		time.Sleep(time.Millisecond * 10)
	}

	stage = "closewait"
	loggo.Debug("close rudp conn update %s", c.Info())

	startEndTime := time.Now()
	for !wg.IsExit() {
		now := time.Now()

		diffclose := now.Sub(startEndTime)
		if diffclose > time.Millisecond*time.Duration(c.config.CloseWaitTimeoutMs) {
			loggo.Debug("close wait conn had timeout %s", c.Info())
			break
		}

		if fm.GetRecvBufferSize() <= 0 {
			loggo.Debug("conn had no data %s", c.Info())
			break
		}

		time.Sleep(time.Millisecond * 10)
	}

	loggo.Debug("close rudp conn %s", c.Info())

	return errors.New("closed " + reason)
}
