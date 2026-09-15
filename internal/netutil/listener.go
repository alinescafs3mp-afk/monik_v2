package netutil

import (
	"fmt"
	"net"
	"net/netip"
	"strconv"
)

// CanonicalListenerTarget never resolves a name or makes a network request.
// Families and interface-scoped IPv6 addresses remain distinct. IPv4-mapped
// IPv6 is canonicalized to IPv4; wildcard binding is normalized by discovery.
func CanonicalListenerTarget(target string) (string, error) {
	host, port, err := net.SplitHostPort(target)
	if err != nil {
		return "", fmt.Errorf("invalid listener target")
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || ip.IsUnspecified() || ip.IsMulticast() {
		return "", fmt.Errorf("listener target must be a concrete IP address")
	}
	p, err := strconv.Atoi(port)
	if err != nil || p < 1 || p > 65535 {
		return "", fmt.Errorf("invalid listener port")
	}
	return net.JoinHostPort(ip.Unmap().String(), strconv.Itoa(p)), nil
}
