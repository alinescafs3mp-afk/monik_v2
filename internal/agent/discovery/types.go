package discovery

import "net"

type Listener struct {
	IP      net.IP
	Port    int
	Inode   string
	PID     int
	Process string
}
