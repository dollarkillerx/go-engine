package conn

import (
	"encoding/binary"
	"errors"
	"github.com/esrrhs/go-engine/src/common"
	"github.com/esrrhs/go-engine/src/congestion"
	"github.com/esrrhs/go-engine/src/frame"
	"github.com/esrrhs/go-engine/src/group"
	"github.com/esrrhs/go-engine/src/loggo"
	"github.com/golang/protobuf/proto"
	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"math"
	"math/rand"
	"net"
	"sync"
	"time"
)

type RicmpConfig struct {
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
	Congestion         string
}

func DefaultRicmpConfig() *RicmpConfig {
	return &RicmpConfig{
		MaxPacketSize:      2048,
		CutSize:            800,
		MaxId:              1000000,
		BufferSize:         1024 * 1024,
		MaxWin:             10000,
		ResendTimems:       200,
		Compress:           0,
		Stat:               0,
		ConnectTimeoutMs:   10000,
		CloseTimeoutMs:     5000,
		CloseWaitTimeoutMs: 5000,
		AcceptChanLen:      128,
		Congestion:         "bb",
	}
}

type ricmpConn struct {
	info          string
	id            string
	config        *RicmpConfig
	dialer        *ricmpConnDialer
	listenersonny *ricmpConnListenerSonny
	listener      *ricmpConnListener
	isclose       bool
	closelock     sync.Mutex
}

type ricmpConnDialer struct {
	serveraddr *net.IPAddr
	conn       *icmp.PacketConn
	fm         *frame.FrameMgr
	wg         *group.Group
	icmpId     int
	icmpSeq    int
	icmpProto  int
	icmpFlag   IcmpMsg_TYPE
}

type ricmpConnListenerSonny struct {
	dstaddr    net.Addr
	fatherconn *icmp.PacketConn
	fm         *frame.FrameMgr
	wg         *group.Group
	icmpId     int
	icmpSeq    int
	icmpProto  int
	icmpFlag   IcmpMsg_TYPE
}

type ricmpConnListener struct {
	listenerconn *icmp.PacketConn
	wg           *group.Group
	sonny        sync.Map
	accept       *common.Channel
}

func (c *ricmpConn) Name() string {
	return "ricmp"
}

func (c *ricmpConn) Read(p []byte) (n int, err error) {
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

func (c *ricmpConn) Write(p []byte) (n int, err error) {
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

func (c *ricmpConn) Close() error {
	c.checkConfig()

	if c.isclose {
		return nil
	}

	c.closelock.Lock()
	defer c.closelock.Unlock()

	loggo.Debug("start Close %s", c.Info())

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
				u := value.(*ricmpConn)
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

func (c *ricmpConn) Info() string {
	c.checkConfig()

	if c.info != "" {
		return c.info
	}
	if c.dialer != nil {
		c.info = c.dialer.conn.LocalAddr().String() + "<--ricmp " + c.id + "-->" + c.dialer.serveraddr.String()
	} else if c.listener != nil {
		c.info = "ricmp " + c.id + "--" + c.listener.listenerconn.LocalAddr().String()
	} else if c.listenersonny != nil {
		c.info = c.listenersonny.fatherconn.LocalAddr().String() + "<--ricmp " + c.id + "-->" + c.listenersonny.dstaddr.String()
	} else {
		c.info = "empty ricmp conn"
	}
	return c.info
}

func (c *ricmpConn) Dial(dst string) (Conn, error) {
	c.checkConfig()

	addr, err := net.ResolveIPAddr("ip", dst)
	if err != nil {
		return nil, err
	}

	conn, err := icmp.ListenPacket("ip4:icmp", "")
	if err != nil {
		return nil, err
	}

	id := common.Guid()
	fm := frame.NewFrameMgr(c.config.CutSize, c.config.MaxId, c.config.BufferSize, c.config.MaxWin, c.config.ResendTimems, c.config.Compress, c.config.Stat)
	fm.SetDebugid(id)
	if c.config.Congestion == "bb" {
		fm.SetCongestion(&congestion.BBCongestion{})
	}

	dialer := &ricmpConnDialer{serveraddr: addr, conn: conn, fm: fm,
		icmpId: rand.Intn(math.MaxInt16), icmpSeq: 0, icmpProto: int(IcmpMsg_PING_PROTO), icmpFlag: IcmpMsg_CLIENT_SEND_FLAG}

	u := &ricmpConn{id: common.UniqueId(), config: c.config, dialer: dialer}

	loggo.Debug("start connect remote ricmp %s %s", u.Info(), id)

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
			u.send_icmp(u.dialer.conn, mb, u.dialer.serveraddr,
				u.id, u.dialer.icmpId, u.dialer.icmpSeq, u.dialer.icmpProto, u.dialer.icmpFlag)
		}

		// recv udp
		u.dialer.conn.SetReadDeadline(time.Now().Add(time.Millisecond * 100))
		n, _, _, id, echoId, _, echoFlag := u.recv_icmp(u.dialer.conn, buf)
		if n > 0 && id == u.id && echoId == u.dialer.icmpId && echoFlag == int(IcmpMsg_SERVER_SEND_FLAG) {
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
			loggo.Debug("can not connect remote ricmp %s", u.Info())
			break
		}

		// timeout
		now := time.Now()
		diffclose := now.Sub(startConnectTime)
		if diffclose > time.Millisecond*time.Duration(c.config.ConnectTimeoutMs) {
			loggo.Debug("can not connect remote ricmp %s", u.Info())
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

	loggo.Debug("connect remote ok ricmp %s", u.Info())

	wg := group.NewGroup("ricmpConn serveListenerSonny"+" "+u.Info(), nil, nil)

	u.dialer.wg = wg

	wg.Go("ricmpConn updateDialerSonny"+" "+u.Info(), func() error {
		return u.updateDialerSonny()
	})

	return u, nil
}

func (c *ricmpConn) Listen(dst string) (Conn, error) {
	c.checkConfig()

	conn, err := icmp.ListenPacket("ip4:icmp", "")
	if err != nil {
		return nil, err
	}

	ch := common.NewChannel(c.config.AcceptChanLen)

	wg := group.NewGroup("ricmpConn Listen"+" "+dst, nil, nil)

	listener := &ricmpConnListener{
		listenerconn: conn,
		wg:           wg,
		accept:       ch,
	}

	u := &ricmpConn{id: common.UniqueId(), config: c.config, listener: listener}
	wg.Go("ricmpConn loopListenerRecv"+" "+dst, func() error {
		return u.loopListenerRecv()
	})

	return u, nil
}

func (c *ricmpConn) Accept() (Conn, error) {
	c.checkConfig()

	if c.listener.wg == nil {
		return nil, errors.New("not listen")
	}
	for !c.listener.wg.IsExit() {
		s := <-c.listener.accept.Ch()
		if s == nil {
			break
		}
		sonny := s.(*ricmpConn)
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

func (c *ricmpConn) checkConfig() {
	if c.config == nil {
		c.config = DefaultRicmpConfig()
	}
}

func (c *ricmpConn) SetConfig(config *RicmpConfig) {
	c.config = config
}

func (c *ricmpConn) loopListenerRecv() error {
	c.checkConfig()

	buf := make([]byte, c.config.MaxPacketSize)
	for !c.listener.wg.IsExit() {
		c.listener.listenerconn.SetReadDeadline(time.Now().Add(time.Millisecond * 100))
		n, srcaddr, err, cid, echoId, echoSeq, _ := c.recv_icmp(c.listener.listenerconn, buf)
		if err != nil {
			continue
		}

		v, ok := c.listener.sonny.Load(cid)
		if !ok {
			id := common.Guid()
			fm := frame.NewFrameMgr(c.config.CutSize, c.config.MaxId, c.config.BufferSize, c.config.MaxWin, c.config.ResendTimems, c.config.Compress, c.config.Stat)
			fm.SetDebugid(id)
			if c.config.Congestion == "bb" {
				fm.SetCongestion(&congestion.BBCongestion{})
			}

			sonny := &ricmpConnListenerSonny{dstaddr: srcaddr, fatherconn: c.listener.listenerconn, fm: fm,
				icmpId: echoId, icmpSeq: echoSeq, icmpProto: int(IcmpMsg_PONG_PROTO), icmpFlag: IcmpMsg_SERVER_SEND_FLAG}

			u := &ricmpConn{id: cid, config: c.config, listenersonny: sonny}
			c.listener.sonny.Store(id, u)

			c.listener.wg.Go("ricmpConn accept"+" "+u.Info(), func() error {
				return c.accept(u)
			})

			loggo.Debug("start accept remote ricmp %s %s", u.Info(), id)
		} else {
			u := v.(*ricmpConn)
			u.listenersonny.icmpSeq = echoSeq

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
			u := value.(*ricmpConn)
			if u.isclose {
				c.listener.sonny.Delete(key)
				loggo.Debug("delete sonny from map %s", u.Info())
			}
			return true
		})
	}
	return nil
}

func (c *ricmpConn) accept(u *ricmpConn) error {

	loggo.Debug("server begin accept ricmp %s", u.Info())

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
			u.send_icmp(u.listenersonny.fatherconn, mb, u.listenersonny.dstaddr,
				u.id, u.listenersonny.icmpId, u.listenersonny.icmpSeq, u.listenersonny.icmpProto, u.listenersonny.icmpFlag)
		}

		now := time.Now()
		diffclose := now.Sub(startConnectTime)
		if diffclose > time.Millisecond*time.Duration(c.config.ConnectTimeoutMs) {
			loggo.Debug("can not connect by remote ricmp %s", u.Info())
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

	loggo.Debug("server accept ricmp ok %s", u.Info())

	c.listener.accept.Write(u)

	wg := group.NewGroup("ricmpConn ListenerSonny"+" "+u.Info(), c.listener.wg, nil)

	u.listenersonny.wg = wg

	wg.Go("ricmpConn updateListenerSonny"+" "+u.Info(), func() error {
		return u.updateListenerSonny()
	})

	loggo.Debug("accept ricmp finish %s", u.Info())

	return nil
}

func (c *ricmpConn) updateListenerSonny() error {
	return c.update_ricmp(c.listenersonny.wg, c.listenersonny.fm, c.listenersonny.fatherconn, c.listenersonny.dstaddr, false,
		0, 0,
		c.id, c.listenersonny.icmpId, c.listenersonny.icmpSeq, c.listenersonny.icmpProto, c.listenersonny.icmpFlag)
}

func (c *ricmpConn) updateDialerSonny() error {
	return c.update_ricmp(c.dialer.wg, c.dialer.fm, c.dialer.conn, c.dialer.serveraddr, true,
		c.dialer.icmpId, int(IcmpMsg_SERVER_SEND_FLAG),
		c.id, c.dialer.icmpId, c.dialer.icmpSeq, c.dialer.icmpProto, c.dialer.icmpFlag)
}

func (c *ricmpConn) update_ricmp(wg *group.Group, fm *frame.FrameMgr, conn *icmp.PacketConn, dstaddr net.Addr, readconn bool,
	recvCheckEchoId int, recvCheckEchoFlag int, id string, icmpId int, icmpSeq int, icmpProto int, icmpFlag IcmpMsg_TYPE) error {

	loggo.Debug("start ricmp conn %s", c.Info())

	stage := "open"

	if readconn {
		wg.Go("ricmpConn update_ricmp recv"+" "+c.Info(), func() error {
			bytes := make([]byte, c.config.MaxPacketSize)
			for !wg.IsExit() && stage != "closewait" {
				// recv udp
				conn.SetReadDeadline(time.Now().Add(time.Millisecond * 100))
				n, _, _, id, echoId, _, echoFlag := c.recv_icmp(conn, bytes)
				if n > 0 && id == c.id && echoId == recvCheckEchoId && echoFlag == recvCheckEchoFlag {
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
			c.send_icmp(conn, mb, dstaddr, id, icmpId, icmpSeq, icmpProto, icmpFlag)
			//loggo.Debug("%s send frame to %s %d", c.Info(), dstaddr, f.Id)
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
	loggo.Debug("close ricmp conn fm %s", c.Info())

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
			c.send_icmp(conn, mb, dstaddr, id, icmpId, icmpSeq, icmpProto, icmpFlag)
			//loggo.Debug("%s send frame to %s %d", c.Info(), dstaddr, f.Id)
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
	loggo.Debug("close ricmp conn update %s", c.Info())

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

	loggo.Debug("close ricmp conn %s", c.Info())

	return errors.New("closed " + reason)
}

func (c *ricmpConn) send_icmp(conn *icmp.PacketConn, data []byte, dst net.Addr, id string, icmpId int, icmpSeq int, icmpProto int, icmpFlag IcmpMsg_TYPE) {

	m := &IcmpMsg{
		Id:    id,
		Data:  data,
		Magic: IcmpMsg_MAGIC,
		Flag:  icmpFlag,
	}

	mb, err := proto.Marshal(m)
	if err != nil {
		loggo.Error("sendICMP Marshal MyMsg error %s %s", c.Info(), err)
		return
	}

	body := &icmp.Echo{
		ID:   icmpId,
		Seq:  icmpSeq,
		Data: mb,
	}

	msg := &icmp.Message{
		Type: (ipv4.ICMPType)(icmpProto),
		Code: 0,
		Body: body,
	}

	bytes, err := msg.Marshal(nil)
	if err != nil {
		loggo.Error("sendICMP Marshal error %s %s", c.Info(), err)
		return
	}

	conn.WriteTo(bytes, dst)
}

func (c *ricmpConn) recv_icmp(conn *icmp.PacketConn, bytes []byte) (int, net.Addr, error, string, int, int, int) {
	n, srcaddr, err := conn.ReadFrom(bytes)

	if err != nil {
		return 0, srcaddr, err, "", 0, 0, 0
	}

	if n <= 0 {
		return 0, srcaddr, errors.New("n <= 0"), "", 0, 0, 0
	}

	echoId := int(binary.BigEndian.Uint16(bytes[4:6]))
	echoSeq := int(binary.BigEndian.Uint16(bytes[6:8]))

	my := &IcmpMsg{}
	err = proto.Unmarshal(bytes[8:n], my)
	if err != nil {
		return 0, srcaddr, err, "", 0, 0, 0
	}

	if my.Magic != IcmpMsg_MAGIC {
		return 0, srcaddr, errors.New("magic error"), "", 0, 0, 0
	}

	copy(bytes, my.Data)

	return len(my.Data), srcaddr, nil, my.Id, echoId, echoSeq, int(my.Flag)
}
