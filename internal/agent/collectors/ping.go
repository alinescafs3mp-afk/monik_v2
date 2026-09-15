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
	target  string
	failure string
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
	rtt, sent, err := pingOnce(ctx, target, timeout)
	p.record(target, now, rtt, sent, err)
}

func (p *Pinger) record(target string, now time.Time, rtt time.Duration, sent bool, err error) {
	s := sample{target: target, at: now, sent: sent}
	if err != nil {
		switch {
		case os.IsPermission(err) || isPerm(err):
			s.permErr = true
			s.failure = "permission_denied"
		case errors.Is(err, context.Canceled):
			s.failure = "cancelled"
		default:
			if sent {
				s.failure = "no_reply"
			} else {
				s.failure = "send_failed"
			}
		}
	} else if sent {
		s.ok = true
		s.rtt = rtt
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	// Keep bounded storage even if a caller temporarily does not request Summary.
	cut := now.Add(-protocol.PingWindow)
	kept := p.samples[:0]
	for _, old := range p.samples {
		if old.target == target && old.at.After(cut) && !old.at.After(now) {
			kept = append(kept, old)
		}
	}
	p.samples = append(kept, s)
	if len(p.samples) > 120 {
		p.samples = p.samples[len(p.samples)-120:]
	}
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
	kept := make([]sample, 0, len(p.samples))
	sent, recv := 0, 0
	var sum, minV, maxV time.Duration
	var latest *sample
	var lastReply *time.Time
	for _, s := range p.samples {
		if !s.at.After(cut) || s.at.After(now) || (s.target != "" && s.target != target) {
			continue
		}
		kept = append(kept, s)
		if latest == nil || !s.at.Before(latest.at) {
			copy := s
			latest = &copy
		}
		if s.sent {
			sent++
		}
		if s.ok && s.sent {
			recv++
			sum += s.rtt
			if recv == 1 || s.rtt < minV {
				minV = s.rtt
			}
			if recv == 1 || s.rtt > maxV {
				maxV = s.rtt
			}
			if lastReply == nil || s.at.After(*lastReply) {
				copy := s.at
				lastReply = &copy
			}
		}
	}
	p.samples = kept
	out := &protocol.PingSummary{Target: target, Sent: sent, Received: recv, WindowSec: int(window.Seconds()), LastReplyAt: lastReply}
	if recv > 0 {
		mean := float64(sum) / float64(time.Millisecond) / float64(recv)
		mn := float64(minV) / float64(time.Millisecond)
		mx := float64(maxV) / float64(time.Millisecond)
		out.MeanMS = &mean
		out.MinMS = &mn
		out.MaxMS = &mx
	}
	if sent > 0 {
		loss := float64(sent-recv) * 100 / float64(sent)
		out.LossPct = &loss
	}
	cap := protocol.Capability{Status: protocol.CapSupported, LastSuccess: lastReply}
	switch {
	case latest == nil:
		out.Status = "pending"
		out.Reason = "Ожидаем первое измерение ICMP"
		cap.Status = protocol.CapPartial
	case latest.permErr:
		out.Status = "permission_denied"
		out.Permission = "permission_denied"
		out.Reason = "Службе запрещён ICMP: проверьте разрешение ping-сокетов или CAP_NET_RAW"
		cap.Status = protocol.CapPermissionDenied
	case !latest.sent:
		out.Status = "send_failed"
		out.Reason = "Не удалось отправить ICMP: проверьте адрес, маршрут и сеть"
		cap.Status = protocol.CapError
	case !latest.ok:
		out.Status = "no_reply"
		out.Reason = "ICMP отправлен, ответ не получен: возможна фильтрация или потеря связи"
	default:
		out.Status = "ok"
	}
	cap.Reason = out.Reason
	return out, cap
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
