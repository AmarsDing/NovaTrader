//go:build windows
// +build windows

package tcp

import (
	"fmt"
	"net"
	"time"
)

// idleTime  检测周期  count  重复次数  interval  检测间隔
func SetKeepAlive(c net.Conn, idleTime, count, interval time.Duration) (err error) {

	conn, ok := c.(*net.TCPConn)
	if !ok {
		return fmt.Errorf("Bad connection type: %T", c)
	}

	if err := conn.SetKeepAlive(true); err != nil {
		return err
	}
	conn.SetKeepAlivePeriod(interval * time.Second)
	return nil
}
