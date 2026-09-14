package discovery

import (
	"net"
	"net/url"
	"strings"

	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
)

// DisabledTargets uses only already configured numeric local targets. It does not
// resolve DNS or grant new probing destinations. Another enabled virtual host on
// the same socket remains scheduled independently, but inventory won't probe it.
func DisabledTargets(defs []protocol.CheckDefinition, locals []net.IP) map[string]protocol.CheckDefinition {
	out := map[string]protocol.CheckDefinition{}
	for _, d := range defs {
		if !d.Paused && !d.Ignored {
			continue
		}
		target := d.DialTarget
		if target == "" {
			u, e := url.Parse(d.URL)
			if e != nil {
				continue
			}
			port := u.Port()
			if port == "" {
				port = "80"
				if u.Scheme == "https" {
					port = "443"
				}
			}
			target = net.JoinHostPort(u.Hostname(), port)
		}
		h, port, e := net.SplitHostPort(target)
		if e != nil {
			continue
		}
		ip := net.ParseIP(strings.Trim(h, "[]"))
		if ip == nil {
			continue
		}
		if ip.IsUnspecified() {
			if ip.To4() != nil {
				ip = net.ParseIP("127.0.0.1")
			} else {
				ip = net.ParseIP("::1")
			}
		}
		target = net.JoinHostPort(ip.String(), port)
		out[target] = d
	}
	return out
}
