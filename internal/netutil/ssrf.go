package netutil

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

type ProbePolicy struct {
	AllowLoopback bool
	AllowLocal    bool
	ExtraCIDRs    []*net.IPNet
	ICMPTargets   []string
	AllowRemote   bool
}

func DefaultPolicy() ProbePolicy {
	return ProbePolicy{AllowLoopback: true, AllowLocal: true, ICMPTargets: []string{"8.8.8.8"}}
}

func ParseURL(raw string) (*url.URL, error) {
	if strings.ContainsAny(raw, "\r\n") {
		return nil, fmt.Errorf("invalid url")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if u.User != nil {
		return nil, fmt.Errorf("url userinfo is not allowed")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("unsupported scheme")
	}
	if u.Host == "" {
		return nil, fmt.Errorf("missing host")
	}
	return u, nil
}

func HostIsWildcard(host string) bool {
	h := strings.Trim(host, "[]")
	return h == "0.0.0.0" || h == "::" || h == ""
}

func LocalInterfaceIPs() ([]net.IP, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var out []net.IP
	for _, iface := range ifaces {
		addrs, _ := iface.Addrs()
		for _, a := range addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip != nil && !ip.IsMulticast() {
				out = append(out, ip)
			}
		}
	}
	return out, nil
}

func IsLocalIP(ip net.IP, locals []net.IP) bool {
	if ip.IsLoopback() {
		return true
	}
	for _, l := range locals {
		if l.Equal(ip) {
			return true
		}
	}
	return false
}

func IsMetadata(ip net.IP) bool {
	if ip.Equal(net.ParseIP("169.254.169.254")) {
		return true
	}
	_, ll, _ := net.ParseCIDR("169.254.0.0/16")
	if ll != nil && ll.Contains(ip) {
		return true
	}
	if ip.IsLinkLocalUnicast() || ip.IsMulticast() || ip.IsUnspecified() {
		return true
	}
	return false
}

func AllowedDial(host string, policy ProbePolicy, locals []net.IP) (net.IP, error) {
	h, _, err := net.SplitHostPort(host)
	if err != nil {
		h = host
	}
	h = strings.Trim(h, "[]")
	if HostIsWildcard(h) {
		return nil, fmt.Errorf("wildcard bind is not a dial target")
	}
	ip := net.ParseIP(h)
	if ip == nil {
		return nil, fmt.Errorf("dns names require explicit authorization; unresolved %q", h)
	}
	if IsMetadata(ip) && !ip.IsLoopback() {
		return nil, fmt.Errorf("metadata or link-local destination denied")
	}
	if ip.IsLoopback() && policy.AllowLoopback {
		return ip, nil
	}
	if policy.AllowLocal && IsLocalIP(ip, locals) {
		return ip, nil
	}
	for _, n := range policy.ExtraCIDRs {
		if n.Contains(ip) {
			return ip, nil
		}
	}
	if policy.AllowRemote {
		return ip, nil
	}
	return nil, fmt.Errorf("destination %s is outside local probe policy", ip)
}

func ICMPAllowed(target string, policy ProbePolicy) bool {
	for _, t := range policy.ICMPTargets {
		if t == target {
			return true
		}
	}
	return false
}

func FormatDial(ip net.IP, port string) string {
	if ip.To4() == nil {
		return net.JoinHostPort(ip.String(), port)
	}
	return net.JoinHostPort(ip.String(), port)
}

func ExpandWildcard(port string, locals []net.IP) []string {
	var out []string
	seen := map[string]bool{}
	for _, ip := range locals {
		if ip.IsMulticast() || ip.IsUnspecified() {
			continue
		}
		d := FormatDial(ip, port)
		if !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	return out
}
