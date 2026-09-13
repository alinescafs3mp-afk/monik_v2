package collectors

import (
	"context"
	"net"
	"os"
	"sync"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
)

type sample struct {
	at      time.Time
	rtt     time.Duration
	ok      bool
	sent    bool
	permErr bool
}

type Pinger struct {
	mu      sync.Mutex
	samples []sample
	seq     int
}

func NewPinger() *Pinger { return &Pinger{} }

func (p *Pinger) Observe(ctx context.Context, target string, timeout time.Duration, now time.Time) {
	s := sample{at: now, sent: true}
	rtt, err := pingOnce(ctx, target, timeout)
	if err != nil {
		if os.IsPermission(err) || isPerm(err) {
			s.sent = false
			s.permErr = true
		}
	} else {
		s.ok = true
		s.rtt = rtt
	}
	p.mu.Lock()
	p.samples = append(p.samples, s)
	p.mu.Unlock()
}

func isPerm(err error) bool {
	if err == nil {
		return false
	}
	es := err.Error()
	return contains(es, "permission") || contains(es, "operation not permitted") || contains(es, "not permitted")
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || (len(s) > 0 && (indexOf(s, sub) >= 0)))
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func (p *Pinger) Summary(now time.Time, window time.Duration, target string) (*protocol.PingSummary, protocol.Capability) {
	p.mu.Lock()
	defer p.mu.Unlock()
	cut := now.Add(-window)
	var kept []sample
	sent, recv := 0, 0
	var sum time.Duration
	var minV, maxV time.Duration
	perm := false
	first := true
	for _, s := range p.samples {
		if s.at.Before(cut) {
			continue
		}
		kept = append(kept, s)
		if s.permErr {
			perm = true
			continue
		}
		if s.sent {
			sent++
		}
		if s.ok {
			recv++
			sum += s.rtt
			if first || s.rtt < minV {
				minV = s.rtt
			}
			if first || s.rtt > maxV {
				maxV = s.rtt
			}
			first = false
		}
	}
	p.samples = kept
	out := &protocol.PingSummary{Target: target, Sent: sent, Received: recv, WindowSec: int(window.Seconds())}
	if perm && sent == 0 {
		out.Permission = "permission_denied"
		return out, protocol.Capability{Status: protocol.CapPermissionDenied, Reason: "cannot send ICMP"}
	}
	if recv > 0 {
		mean := float64(sum.Microseconds()) / float64(recv) / 1000.0
		mn := float64(minV.Microseconds()) / 1000.0
		mx := float64(maxV.Microseconds()) / 1000.0
		out.MeanMS, out.MinMS, out.MaxMS = &mean, &mn, &mx
	}
	if sent > 0 {
		loss := float64(sent-recv) / float64(sent) * 100
		out.LossPct = &loss
	}
	t := now
	return out, protocol.Capability{Status: protocol.CapSupported, LastSuccess: &t}
}

func pingOnce(ctx context.Context, target string, timeout time.Duration) (time.Duration, error) {
	ip := net.ParseIP(target)
	if ip == nil {
		ips, err := net.DefaultResolver.LookupIP(ctx, "ip4", target)
		if err != nil || len(ips) == 0 {
			return 0, err
		}
		ip = ips[0]
	}
	c, err := icmp.ListenPacket("udp4", "0.0.0.0")
	if err != nil {
		c, err = icmp.ListenPacket("ip4:icmp", "0.0.0.0")
		if err != nil {
			return 0, err
		}
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(timeout))
	wm := icmp.Message{Type: ipv4.ICMPTypeEcho, Code: 0, Body: &icmp.Echo{ID: os.Getpid() & 0xffff, Seq: 1, Data: []byte("monik")}}
	wb, err := wm.Marshal(nil)
	if err != nil {
		return 0, err
	}
	dst := &net.IPAddr{IP: ip}
	start := time.Now()
	if _, err := c.WriteTo(wb, dst); err != nil {
		if ua, ok := tryUDP(ip); ok {
			if _, err2 := c.WriteTo(wb, ua); err2 != nil {
				return 0, err
			}
		} else {
			return 0, err
		}
	}
	rb := make([]byte, 1500)
	n, _, err := c.ReadFrom(rb)
	if err != nil {
		return 0, err
	}
	_, err = icmp.ParseMessage(1, rb[:n])
	if err != nil {
		return time.Since(start), nil
	}
	return time.Since(start), nil
}

func tryUDP(ip net.IP) (*net.UDPAddr, bool) {
	return &net.UDPAddr{IP: ip}, true
}
