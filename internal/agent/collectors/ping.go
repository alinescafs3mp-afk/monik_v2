package collectors

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
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
	rtt, sent, err := pingOnce(ctx, target, timeout)
	s.sent = sent
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
		if !s.at.After(cut) || s.at.After(now) {
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

// Only a reply to this particular request is evidence of reachability. UDP
// ping sockets can have their ID rewritten by the kernel, hence the nonce
// and source/sequence checks remain mandatory even in that mode.
func matchesReply(packet []byte, peer net.Addr, target net.IP, id, seq int, nonce []byte, raw bool) bool {
	var source net.IP
	switch p := peer.(type) {
	case *net.IPAddr:
		source = p.IP
	case *net.UDPAddr:
		source = p.IP
	}
	if source == nil || !source.Equal(target) {
		return false
	}
	m, err := icmp.ParseMessage(1, packet)
	if err != nil || m.Type != ipv4.ICMPTypeEchoReply || m.Code != 0 {
		return false
	}
	e, ok := m.Body.(*icmp.Echo)
	return ok && e.Seq == seq && (!raw || e.ID == id) && bytes.Equal(e.Data, nonce)
}

func pingOnce(ctx context.Context, target string, timeout time.Duration) (time.Duration, bool, error) {
	ip := net.ParseIP(target)
	if ip == nil {
		ips, err := net.DefaultResolver.LookupIP(ctx, "ip4", target)
		if err != nil {
			return 0, false, err
		}
		if len(ips) == 0 {
			return 0, false, fmt.Errorf("no IPv4 address")
		}
		ip = ips[0]
	}
	if ip.To4() == nil {
		return 0, false, fmt.Errorf("IPv4 ICMP target required")
	}
	c, err := icmp.ListenPacket("udp4", "0.0.0.0")
	raw := false
	if err != nil {
		raw = true
		c, err = icmp.ListenPacket("ip4:icmp", "0.0.0.0")
		if err != nil {
			return 0, false, err
		}
	}
	defer c.Close()
	deadline := time.Now().Add(timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := ctx.Err(); err != nil {
		return 0, false, err
	}
	if err := c.SetDeadline(deadline); err != nil {
		return 0, false, err
	}
	stop := context.AfterFunc(ctx, func() { _ = c.SetDeadline(time.Now()) })
	defer stop()
	nonce := make([]byte, 24)
	if _, err := rand.Read(nonce); err != nil {
		return 0, false, err
	}
	id, seq := os.Getpid()&0xffff, 1
	wm := icmp.Message{Type: ipv4.ICMPTypeEcho, Code: 0, Body: &icmp.Echo{ID: id, Seq: seq, Data: nonce}}
	wb, err := wm.Marshal(nil)
	if err != nil {
		return 0, false, err
	}
	var dst net.Addr = &net.UDPAddr{IP: ip}
	if raw {
		dst = &net.IPAddr{IP: ip}
	}
	start := time.Now()
	n, err := c.WriteTo(wb, dst)
	if err != nil {
		return 0, false, err
	}
	if n != len(wb) {
		return 0, false, errors.New("short ICMP write")
	}
	buf := make([]byte, 1500)
	for {
		n, peer, err := c.ReadFrom(buf)
		if err != nil {
			return 0, true, err
		}
		if matchesReply(buf[:n], peer, ip, id, seq, nonce, raw) {
			return time.Since(start), true, nil
		}
	}
}
