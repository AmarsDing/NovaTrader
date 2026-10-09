package udpserver

import (
	"net"

	"golang.org/x/net/ipv4"
)

type UdpServer struct {
	Conn          *net.UDPConn // udp conn
	LocalAddress  string       // UDP server listening address.
	RemoteAddress string       // 远程ip地址
	NetName       string       // 网卡名称
	TotalSend     int64        // 发送总字节数
	TotalRecv     int64        // 接收总字节数
}

func NewServer(netName, laddress, remote string) (*UdpServer, error) {

	s := &UdpServer{
		LocalAddress:  laddress,
		RemoteAddress: remote,
		NetName:       netName,
	}
	err := s.initConn()
	if err != nil {
		return nil, err
	}
	return s, nil
}

func (us *UdpServer) initConn() error {
	if us.Conn != nil {
		return nil
	}
	laddr, err := net.ResolveUDPAddr("udp", us.LocalAddress)
	if err != nil {
		return err
	}
	raddr, err := net.ResolveUDPAddr("udp", us.RemoteAddress)
	if err != nil {
		return err
	}
	Conn, err := net.DialUDP("udp4", laddr, raddr)
	if err != nil {
		return err
	}
	pc := ipv4.NewPacketConn(Conn)
	if err := pc.SetMulticastLoopback(true); err != nil {
		return err
	}
	us.Conn = Conn
	return nil
}

func (us *UdpServer) Send(data []byte) error {
	n, err := us.Conn.Write(data)
	if err != nil {
		return err
	}
	us.TotalSend += int64(n)
	return nil
}

func (us *UdpServer) Stop() {
	us.TotalRecv = 0
	us.TotalSend = 0
	us.Conn.Close()
}
