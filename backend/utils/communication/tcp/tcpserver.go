package tcp

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"

	"github.com/go-kratos/kratos/v2/log"
)

var ClientMgrMutex sync.Mutex

type TcpServer struct {
	// id
	Id int
	// 服务名称
	Name string
	// 服务版本 ipv4  ipv6
	Version string
	// 地址
	Addr string
	// 服务监听地址
	Listner *net.TCPListener
	// 客户端管理
	ClientManager map[int64]*TcpConn
	// 客户端数量
	Count int64
	log   *log.Helper
}

func NewTcpServer(id int, name, version, addr string, logger *log.Helper) *TcpServer {
	return &TcpServer{
		Id:            id,
		Name:          name,
		Version:       version,
		Addr:          addr,
		Listner:       nil,
		ClientManager: make(map[int64]*TcpConn),
		Count:         0,
		log:           logger,
	}
}

func (ts *TcpServer) Start(datach chan []byte) error {
	listner, err := ts.createListner()
	if err != nil {
		return err
	}
	ts.Listner = listner
	go ts.Accept(datach)
	return nil
}

func (ts *TcpServer) Stop() {
	ts.log.Error("tcp server conn stop,server=", ts.Addr, "client=", ts.ClientManager)
	for _, conn := range ts.ClientManager {
		if conn != nil {
			conn.Close()
			ts.log.Error("tcp server 中 conn stop,server=", ts.Addr, "client=", conn.Conn.RemoteAddr())
		}
	}
	ts.Count = 0
	ts.Listner.Close()
	ts.log.Error("tcp server stop,server=", ts.Addr)
}

func (ts *TcpServer) createListner() (*net.TCPListener, error) {
	if ts.Addr == "" || ts.Version == "" {
		return nil, errors.New("地址和ip版本为空")
	}
	addr, err := net.ResolveTCPAddr(ts.Version, ts.Addr)
	if err != nil {
		return nil, err
	}
	// 创建tcp服务
	listener, err := net.ListenTCP(ts.Version, addr)
	if err != nil {
		return nil, err
	}
	return listener, nil
}

func (ts *TcpServer) Accept(datach chan []byte) {
	defer func() {
		if ts.Listner != nil {
			ts.Listner.Close()
		}
	}()
	for {
		conn, err := ts.Listner.AcceptTCP()
		if err != nil {
			if strings.Contains(err.Error(), "use of closed network connection") {
				return
			}
			fmt.Println("Accept err:", err)
			continue
		}
		err = SetKeepAlive(conn, 70, 3, 5)
		if err != nil {
			continue
		}
		ts.Count++
		ts.AddClient(ts.Count, conn)
		// 启动读取线程
		go ts.read(datach, conn)
	}
}

func (ts *TcpServer) read(datach chan []byte, conn *net.TCPConn) {
	data := make([]byte, 1024)
	for {
		n, err := conn.Read(data)
		if err != nil {
			ts.log.Errorf("tcp server read error, server=%s,client=%s,errors=%s", ts.Addr, conn.RemoteAddr(), err.Error())
			return
		}
		datach <- data[:n]
	}
}

func (ts *TcpServer) RemoveClient(id int64) {
	ClientMgrMutex.Lock()
	defer ClientMgrMutex.Unlock()
	delete(ts.ClientManager, id)
}

func (ts *TcpServer) AddClient(id int64, conn *net.TCPConn) {
	ClientMgrMutex.Lock()
	defer ClientMgrMutex.Unlock()
	c := &TcpConn{
		Id:   id,
		Conn: conn,
		Ts:   ts,
	}
	ts.ClientManager[id] = c
}
