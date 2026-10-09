package tcp

import "net"

type TcpClient struct {
	LocalAddr  string       // 本地网卡地址
	ServerAddr string       // 要访问服务器的ip地址
	TotalSend  int64        // 发送总字节
	TotalRecv  int64        // 接收总字节
	Conn       *net.TCPConn // tcp conn

}

func NewTcpClient(laddr, serveraddr string) *TcpClient {
	tc := &TcpClient{
		LocalAddr:  laddr,
		ServerAddr: serveraddr,
		Conn:       nil,
	}
	return tc
}

func (tc *TcpClient) CreateClient() error {
	laddr, err := net.ResolveTCPAddr("tcp", tc.LocalAddr)
	if err != nil {
		return err
	}
	raddr, err := net.ResolveTCPAddr("tcp", tc.ServerAddr)
	if err != nil {
		return err
	}
	Conn, err := net.DialTCP("tcp4", laddr, raddr)
	if err != nil {
		return err
	}
	tc.Conn = Conn
	return nil
}

func (tc *TcpClient) Stop() {
	tc.TotalRecv = 0
	tc.TotalSend = 0
	tc.Conn.Close()
	tc.Conn = nil
}

func (tc *TcpClient) Send(data []byte) error {
	n, err := tc.Conn.Write(data)
	if err != nil {
		return err
	}
	tc.TotalSend += int64(n)
	return nil
}
