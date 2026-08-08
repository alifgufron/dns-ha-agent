package health

import (
	"net"
	"time"
)

func CheckTCP(address string, timeout time.Duration) bool {
	if address == "" {
		address = "127.0.0.1:53"
	}
	conn, err := net.DialTimeout("tcp", address, timeout)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}
