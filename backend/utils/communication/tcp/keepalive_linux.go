package tcp

// +build linux

import (
	"server/utils/communication/tcp/tcpkeepalive"
	"net"
	"time"
)

// idleTime  检测周期  count  重复次数  interval  检测间隔
func SetKeepAlive(conn *net.TCPConn, idleTime, interval time.Duration, count int) (err error) {

	err = tcpkeepalive.SetKeepAlive(conn, idleTime, count, interval)

	if err != nil {
		return err
	}
	return nil
}
