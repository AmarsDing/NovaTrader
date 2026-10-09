package tcp

import "net"

type TcpConn struct {
	Id   int64        // id
	Ts   *TcpServer   // server
	Conn *net.TCPConn // conn
}

func NewTcpConn(id int64, ts *TcpServer, conn *net.TCPConn) *TcpConn {
	return &TcpConn{
		Id:   id,
		Conn: conn,
	}
}

func (tc *TcpConn) Close() {
	if tc.Conn != nil {
		tc.Conn.Close()
	}
	tc.Ts.RemoveClient(tc.Id)
}
