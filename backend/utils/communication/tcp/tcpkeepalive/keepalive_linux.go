package tcpkeepalive

import (
	"os"
	"syscall"
)

func setIdle(fd int, secs int) error {
	// fmt.Println(secs)
	return os.NewSyscallError("setsockopt", syscall.SetsockoptInt(fd, syscall.IPPROTO_TCP, syscall.TCP_KEEPIDLE, secs))
}

func setCount(fd int, n int) error {
	// fmt.Println(n)
	return os.NewSyscallError("setsockopt", syscall.SetsockoptInt(fd, syscall.IPPROTO_TCP, syscall.TCP_KEEPCNT, n))
}

func setInterval(fd int, secs int) error {
	// fmt.Println(secs)
	return os.NewSyscallError("setsockopt", syscall.SetsockoptInt(fd, syscall.IPPROTO_TCP, syscall.TCP_KEEPINTVL, secs))
}
