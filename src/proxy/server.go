package proxy

import (
	"errors"
	"github.com/esrrhs/go-engine/src/common"
	"github.com/esrrhs/go-engine/src/conn"
	"github.com/esrrhs/go-engine/src/group"
	"github.com/esrrhs/go-engine/src/loggo"
	"strconv"
	"sync"
	"sync/atomic"
)

type ClientConn struct {
	ProxyConn

	proxyproto PROXY_PROTO
	clienttype CLIENT_TYPE
	fromaddr   string
	toaddr     string
	name       string

	input  *Inputer
	output *Outputer
}

type Server struct {
	config     *Config
	listenaddr string
	listenConn conn.Conn
	wg         *group.Group
	clients    sync.Map
}

func NewServer(config *Config, listenaddr string) (*Server, error) {

	if config == nil {
		config = DefaultConfig()
	}

	conn, err := conn.NewConn(config.Proto)
	if conn == nil {
		return nil, err
	}

	listenConn, err := conn.Listen(listenaddr)
	if err != nil {
		return nil, err
	}

	wg := group.NewGroup("Server"+" "+listenaddr, nil, func() {
		loggo.Info("group start exit %s", listenConn.Info())
		listenConn.Close()
		loggo.Info("group start exit %s", listenConn.Info())
	})

	s := &Server{
		config:     config,
		listenaddr: listenaddr,
		listenConn: listenConn,
		wg:         wg,
	}

	wg.Go("Server listen"+" "+listenaddr, func() error {
		atomic.AddInt32(&gStateThreadNum.ThreadNum, 1)
		defer atomic.AddInt32(&gStateThreadNum.ThreadNum, -1)
		return s.listen()
	})

	wg.Go("Client state"+" "+listenaddr, func() error {
		return showState(wg)
	})

	return s, nil
}

func (s *Server) Close() {
	s.wg.Stop()
	s.wg.Wait()
}

func (s *Server) listen() error {
	loggo.Info("listen start %s", s.listenaddr)
	for !s.wg.IsExit() {
		conn, err := s.listenConn.Accept()
		if err != nil {
			loggo.Info("Server listen Accept fail %s", err)
			continue
		}

		size := s.clientSize()
		if size >= s.config.MaxClient {
			loggo.Info("Server listen max client %s %d", conn.Info(), size)
			conn.Close()
			continue
		}

		clientconn := &ClientConn{ProxyConn: ProxyConn{conn: conn}}
		s.wg.Go("Server serveClient"+" "+conn.Info(), func() error {
			atomic.AddInt32(&gStateThreadNum.ThreadNum, 1)
			defer atomic.AddInt32(&gStateThreadNum.ThreadNum, -1)
			return s.serveClient(clientconn)
		})
	}
	loggo.Info("listen end %s", s.listenaddr)
	return nil
}

func (s *Server) clientSize() int {
	size := 0
	s.clients.Range(func(key, value interface{}) bool {
		size++
		return true
	})
	return size
}

func (s *Server) serveClient(clientconn *ClientConn) error {

	loggo.Info("serveClient accept new client %s", clientconn.conn.Info())

	sendch := common.NewChannel(s.config.MainBuffer)
	recvch := common.NewChannel(s.config.MainBuffer)

	clientconn.sendch = sendch
	clientconn.recvch = recvch

	wg := group.NewGroup("Server serveClient"+" "+clientconn.conn.Info(), s.wg, func() {
		loggo.Info("group start exit %s", clientconn.conn.Info())
		clientconn.conn.Close()
		sendch.Close()
		recvch.Close()
		if clientconn.input != nil {
			clientconn.input.Close()
		}
		if clientconn.output != nil {
			clientconn.output.Close()
		}
		loggo.Info("group end exit %s", clientconn.conn.Info())
	})

	wg.Go("Server recvFrom"+" "+clientconn.conn.Info(), func() error {
		atomic.AddInt32(&gStateThreadNum.ThreadNum, 1)
		defer atomic.AddInt32(&gStateThreadNum.ThreadNum, -1)
		return recvFrom(wg, recvch, clientconn.conn, s.config.MaxMsgSize, s.config.Encrypt)
	})

	wg.Go("Server sendTo"+" "+clientconn.conn.Info(), func() error {
		atomic.AddInt32(&gStateThreadNum.ThreadNum, 1)
		defer atomic.AddInt32(&gStateThreadNum.ThreadNum, -1)
		return sendTo(wg, sendch, clientconn.conn, s.config.Compress, s.config.MaxMsgSize, s.config.Encrypt)
	})

	wg.Go("Server checkPingActive"+" "+clientconn.conn.Info(), func() error {
		atomic.AddInt32(&gStateThreadNum.ThreadNum, 1)
		defer atomic.AddInt32(&gStateThreadNum.ThreadNum, -1)
		return checkPingActive(wg, sendch, recvch, &clientconn.ProxyConn, s.config.EstablishedTimeout, s.config.PingInter, s.config.PingTimeoutInter, s.config.ShowPing)
	})

	wg.Go("Server checkNeedClose"+" "+clientconn.conn.Info(), func() error {
		atomic.AddInt32(&gStateThreadNum.ThreadNum, 1)
		defer atomic.AddInt32(&gStateThreadNum.ThreadNum, -1)
		return checkNeedClose(wg, &clientconn.ProxyConn)
	})

	wg.Go("Server process"+" "+clientconn.conn.Info(), func() error {
		atomic.AddInt32(&gStateThreadNum.ThreadNum, 1)
		defer atomic.AddInt32(&gStateThreadNum.ThreadNum, -1)
		return s.process(wg, sendch, recvch, clientconn)
	})

	wg.Wait()
	if clientconn.established {
		s.clients.Delete(clientconn.name)
	}

	loggo.Info("serveClient close client %s", clientconn.conn.Info())

	return nil
}

func (s *Server) process(wg *group.Group, sendch *common.Channel, recvch *common.Channel, clientconn *ClientConn) error {

	loggo.Info("process start %s", clientconn.conn.Info())

	for !wg.IsExit() {
		ff := <-recvch.Ch()
		if ff == nil {
			break
		}
		f := ff.(*ProxyFrame)
		switch f.Type {
		case FRAME_TYPE_LOGIN:
			s.processLogin(wg, f, sendch, clientconn)

		case FRAME_TYPE_PING:
			processPing(f, sendch, &clientconn.ProxyConn)

		case FRAME_TYPE_PONG:
			processPong(f, sendch, &clientconn.ProxyConn, s.config.ShowPing)

		case FRAME_TYPE_DATA:
			s.processData(f, clientconn)

		case FRAME_TYPE_OPEN:
			s.processOpen(f, clientconn)

		case FRAME_TYPE_OPENRSP:
			s.processOpenRsp(f, clientconn)

		case FRAME_TYPE_CLOSE:
			s.processClose(f, clientconn)
		}
	}
	loggo.Info("process end %s", clientconn.conn.Info())
	return nil
}

func (s *Server) processLogin(wg *group.Group, f *ProxyFrame, sendch *common.Channel, clientconn *ClientConn) {
	loggo.Info("processLogin from %s %s", clientconn.conn.Info(), f.LoginFrame.String())

	clientconn.proxyproto = f.LoginFrame.Proxyproto
	clientconn.clienttype = f.LoginFrame.Clienttype
	clientconn.fromaddr = f.LoginFrame.Fromaddr
	clientconn.toaddr = f.LoginFrame.Toaddr
	clientconn.name = f.LoginFrame.Name

	rf := &ProxyFrame{}
	rf.Type = FRAME_TYPE_LOGINRSP
	rf.LoginRspFrame = &LoginRspFrame{}

	if f.LoginFrame.Key != s.config.Key {
		rf.LoginRspFrame.Ret = false
		rf.LoginRspFrame.Msg = "key error"
		sendch.Write(rf)
		loggo.Error("processLogin fail key error %s %s", clientconn.conn.Info(), f.LoginFrame.String())
		return
	}

	if clientconn.established {
		rf.LoginRspFrame.Ret = false
		rf.LoginRspFrame.Msg = "has established before"
		sendch.Write(rf)
		loggo.Error("processLogin fail has established before %s %s", clientconn.conn.Info(), f.LoginFrame.String())
		return
	}

	_, loaded := s.clients.LoadOrStore(f.LoginFrame.Name, clientconn)
	if loaded {
		rf.LoginRspFrame.Ret = false
		rf.LoginRspFrame.Msg = f.LoginFrame.Name + " has login before"
		sendch.Write(rf)
		loggo.Error("processLogin fail %s has login before %s %s", f.LoginFrame.Name, clientconn.conn.Info(), f.LoginFrame.String())
		return
	}

	err := s.iniService(wg, f, clientconn)
	if err != nil {
		s.clients.Delete(clientconn.name)
		rf.LoginRspFrame.Ret = false
		rf.LoginRspFrame.Msg = "iniService fail"
		sendch.Write(rf)
		loggo.Error("processLogin iniService fail %s %s %s", clientconn.conn.Info(), f.LoginFrame.String(), err)
		return
	}

	clientconn.established = true

	rf.LoginRspFrame.Ret = true
	rf.LoginRspFrame.Msg = "ok"
	sendch.Write(rf)

	loggo.Info("processLogin ok %s %s", clientconn.conn.Info(), f.LoginFrame.String())
}

func (s *Server) iniService(wg *group.Group, f *ProxyFrame, clientConn *ClientConn) error {
	switch f.LoginFrame.Clienttype {
	case CLIENT_TYPE_PROXY:
		output, err := NewOutputer(wg, f.LoginFrame.Proxyproto.String(), f.LoginFrame.Clienttype, s.config, &clientConn.ProxyConn)
		if err != nil {
			return err
		}
		clientConn.output = output
	case CLIENT_TYPE_REVERSE_PROXY:
		input, err := NewInputer(wg, f.LoginFrame.Proxyproto.String(), f.LoginFrame.Fromaddr, f.LoginFrame.Clienttype, s.config, &clientConn.ProxyConn, clientConn.toaddr)
		if err != nil {
			return err
		}
		clientConn.input = input
	case CLIENT_TYPE_SOCKS5:
		output, err := NewOutputer(wg, f.LoginFrame.Proxyproto.String(), f.LoginFrame.Clienttype, s.config, &clientConn.ProxyConn)
		if err != nil {
			return err
		}
		clientConn.output = output
	case CLIENT_TYPE_REVERSE_SOCKS5:
		input, err := NewSocks5Inputer(wg, f.LoginFrame.Proxyproto.String(), f.LoginFrame.Fromaddr, f.LoginFrame.Clienttype, s.config, &clientConn.ProxyConn)
		if err != nil {
			return err
		}
		clientConn.input = input
	default:
		return errors.New("error CLIENT_TYPE " + strconv.Itoa(int(f.LoginFrame.Clienttype)))
	}
	return nil
}

func (s *Server) processData(f *ProxyFrame, clientconn *ClientConn) {
	if clientconn.input != nil {
		clientconn.input.processDataFrame(f)
	} else if clientconn.output != nil {
		clientconn.output.processDataFrame(f)
	}
}

func (s *Server) processOpenRsp(f *ProxyFrame, clientconn *ClientConn) {
	if clientconn.input != nil {
		clientconn.input.processOpenRspFrame(f)
	}
}

func (c *Server) processOpen(f *ProxyFrame, clientconn *ClientConn) {
	if clientconn.output != nil {
		clientconn.output.processOpenFrame(f)
	}
}

func (c *Server) processClose(f *ProxyFrame, clientconn *ClientConn) {
	if clientconn.input != nil {
		clientconn.input.processCloseFrame(f)
	} else if clientconn.output != nil {
		clientconn.output.processCloseFrame(f)
	}
}
