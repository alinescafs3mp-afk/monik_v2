package discovery

import "net"

type Listener struct {
	IP      net.IP
	Zone    string
	Port    int
	Inode   string
	PID     int
	Process string
}
