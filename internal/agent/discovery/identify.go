package discovery

import (
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/idgen"
	"github.com/alinescafs3mp-afk/monik_v2/internal/netutil"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
)

func DialTargets(ls []Listener, locals []net.IP) []string {
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, l := range ls {
		port := strconv.Itoa(l.Port)
		if netutil.HostIsWildcard(l.IP.String()) || l.IP.IsUnspecified() {
			for _, d := range netutil.ExpandWildcard(port, locals) {
				add(d)
			}
			continue
		}
		add(netutil.FormatDial(l.IP, port))
	}
	return out
}

func Identify(targets []string, budget int, timeout time.Duration) *protocol.DiscoveryDelta {
	start := time.Now()
	delta := &protocol.DiscoveryDelta{Kind: "snapshot", StartedAt: start, CoverageComplete: true}
	n := 0
	for _, t := range targets {
		if n >= budget {
			delta.Truncated = true
			delta.CoverageComplete = false
			break
		}
		n++
		ep, unresolved := identifyOne(t, timeout)
		if ep != nil {
			delta.Confirmed = append(delta.Confirmed, *ep)
		} else if unresolved != nil {
			delta.Unresolved = append(delta.Unresolved, *unresolved)
		}
	}
	delta.ListenerCount = len(targets)
	delta.EndedAt = time.Now()
	return delta
}

func identifyOne(dial string, timeout time.Duration) (*protocol.DiscoveredEndpoint, *protocol.UnresolvedCandidate) {
	host, port, err := net.SplitHostPort(dial)
	if err != nil {
		return nil, &protocol.UnresolvedCandidate{DialTarget: dial, Reason: "bad dial"}
	}
	if netutil.HostIsWildcard(host) {
		return nil, &protocol.UnresolvedCandidate{DialTarget: dial, Reason: "wildcard not dialed"}
	}
	d := net.Dialer{Timeout: timeout}
	c, err := d.Dial("tcp", dial)
	if err != nil {
		return nil, &protocol.UnresolvedCandidate{DialTarget: dial, Reason: "tcp connect failed"}
	}
	_ = c.Close()

	httpsURL := "https://" + formatHost(host, port)
	httpURL := "http://" + formatHost(host, port)
	if speaks, tlsOK := probeHTTP(httpsURL, timeout, true); speaks {
		return &protocol.DiscoveredEndpoint{
			ServiceID: idgen.New(), DialTarget: dial, URL: httpsURL,
			SpeaksHTTP: true, SpeaksTLS: tlsOK, Source: "listener+https", FirstSeen: time.Now(),
		}, nil
	}
	if speaks, _ := probeHTTP(httpURL, timeout, false); speaks {
		return &protocol.DiscoveredEndpoint{
			ServiceID: idgen.New(), DialTarget: dial, URL: httpURL,
			SpeaksHTTP: true, SpeaksTLS: false, Source: "listener+http", FirstSeen: time.Now(),
		}, nil
	}
	return nil, &protocol.UnresolvedCandidate{DialTarget: dial, Reason: "tcp open; not identified as HTTP"}
}

func formatHost(host, port string) string {
	return net.JoinHostPort(strings.Trim(host, "[]"), port)
}

func probeHTTP(raw string, timeout time.Duration, tls bool) (speaksHTTP bool, tlsOK bool) {
	ok, code := headOrGet(raw, timeout, tls)
	return ok && code > 0, tls && ok
}
