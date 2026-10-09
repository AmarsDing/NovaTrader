package broadcast

import (
	"errors"
	"net"

	"golang.org/x/net/ipv4"
)

type BroadCast struct {
	BroadCastIP   string
	BroadCastPort int
	BindNetWork   string
	BroadCastAddr *net.UDPAddr
	BroadCastConn *net.UDPConn
}

func NewBroadCast(BroadCastIP string, BroadCastPort int, BindNetWork string) (*BroadCast, error) {
	bc := &BroadCast{
		BroadCastIP:   BroadCastIP,
		BroadCastPort: BroadCastPort,
		BindNetWork:   BindNetWork,
	}

	IP := net.ParseIP(bc.BroadCastIP)
	if IP == nil {
		return nil, errors.New("IP地址错误")
	}

	bc.BroadCastAddr = &net.UDPAddr{
		IP:   IP,
		Port: bc.BroadCastPort,
	}

	var err error
	bc.BroadCastConn, err = net.ListenUDP("udp4", bc.BroadCastAddr)
	if err != nil {
		return nil, err
	}

	pc := ipv4.NewPacketConn(bc.BroadCastConn)

	iface, err := net.InterfaceByName(bc.BindNetWork)
	if err != nil {
		return nil, err
	}

	if err = pc.JoinGroup(iface, bc.BroadCastAddr); err != nil {
		return nil, err
	}

	if err := pc.SetMulticastLoopback(true); err != nil {
		return nil, err
	}

	return bc, nil
}
