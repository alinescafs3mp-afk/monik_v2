//go:build !linux

package agentconsole

import (
	"context"
	"fmt"
	"net"
)

func ServeActivated(context.Context, int) error {
	return fmt.Errorf("agent console requires Linux/systemd")
}
func LocalAvailable() bool { return false }
func DialLocal(context.Context) (net.Conn, error) {
	return nil, fmt.Errorf("agent console unavailable on this platform")
}
