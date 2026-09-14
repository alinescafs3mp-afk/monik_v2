package collectors

import (
	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"net"
	"testing"
	"time"
)

func TestReviewPingAuthenticatesEchoEvidence(t *testing.T) {
	ip := net.ParseIP("192.0.2.1")
	peer := &net.IPAddr{IP: ip}
	nonce := []byte("unique-request")
	packet := func(typ ipv4.ICMPType, id, seq int, data []byte) []byte {
		b, e := (&icmp.Message{Type: typ, Code: 0, Body: &icmp.Echo{ID: id, Seq: seq, Data: data}}).Marshal(nil)
		if e != nil {
			t.Fatal(e)
		}
		return b
	}
	valid := packet(ipv4.ICMPTypeEchoReply, 5, 7, nonce)
	if !matchesReply(valid, peer, ip, 5, 7, nonce, true) {
		t.Fatal("valid reply lost")
	}
	for name, b := range map[string][]byte{"malformed": {1, 2}, "echo-request": packet(ipv4.ICMPTypeEcho, 5, 7, nonce), "wrong-id": packet(ipv4.ICMPTypeEchoReply, 6, 7, nonce), "wrong-sequence": packet(ipv4.ICMPTypeEchoReply, 5, 8, nonce), "old-payload": packet(ipv4.ICMPTypeEchoReply, 5, 7, []byte("previous"))} {
		t.Run(name, func(t *testing.T) {
			if matchesReply(b, peer, ip, 5, 7, nonce, true) {
				t.Fatal("unrelated packet accepted")
			}
		})
	}
	if matchesReply(valid, &net.IPAddr{IP: net.ParseIP("192.0.2.2")}, ip, 5, 7, nonce, true) {
		t.Fatal("wrong source")
	}
	if !matchesReply(packet(ipv4.ICMPTypeEchoReply, 999, 7, nonce), peer, ip, 5, 7, nonce, false) {
		t.Fatal("kernel-rewritten UDP id")
	}
}
func TestReviewPingWindowExcludesExpiredAndUnsent(t *testing.T) {
	p := NewPinger()
	now := time.Now()
	for i := 0; i <= 12; i++ {
		p.samples = append(p.samples, sample{at: now.Add(-time.Duration(i) * 5 * time.Second), ok: true, sent: true, rtt: time.Millisecond})
	}
	p.samples = append(p.samples, sample{at: now, sent: false})
	s, _ := p.Summary(now, time.Minute, "192.0.2.1")
	if s.Sent != 12 || s.Received != 12 || s.LossPct == nil || *s.LossPct != 0 {
		t.Fatalf("%+v", s)
	}
}
