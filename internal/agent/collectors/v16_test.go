package collectors

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
)

func TestV16PingWindowNeverMixesTargets(t *testing.T) {
	p := NewPinger()
	now := time.Now()
	p.record("192.0.2.1", now, 10*time.Millisecond, true, nil)
	p.record("192.0.2.2", now.Add(time.Second), 0, true, errors.New("timeout"))
	s, c := p.Summary(now.Add(time.Second), time.Minute, "192.0.2.2")
	if s.Received != 0 || s.MeanMS != nil || s.Sent != 1 || c.LastSuccess != nil {
		t.Fatalf("mixed ping: %+v %+v", s, c)
	}
}
func TestV16PingReportsFailureWithoutFabricatedZeroOrLastSuccess(t *testing.T) {
	for _, v := range []struct {
		name   string
		sent   bool
		err    error
		status string
		cap    protocol.CapabilityStatus
	}{
		{"permission", false, os.ErrPermission, "permission_denied", protocol.CapPermissionDenied},
		{"no reply", true, context.DeadlineExceeded, "no_reply", protocol.CapSupported},
		{"send failure", false, errors.New("unreachable"), "send_failed", protocol.CapError},
	} {
		t.Run(v.name, func(t *testing.T) {
			p := NewPinger()
			now := time.Now()
			p.record("8.8.8.8", now, 0, v.sent, v.err)
			s, c := p.Summary(now, time.Minute, "8.8.8.8")
			if s.Status != v.status || c.Status != v.cap || c.LastSuccess != nil || s.MeanMS != nil {
				t.Fatalf("%+v %+v", s, c)
			}
			if !v.sent && s.LossPct != nil {
				t.Fatal("unsent was loss")
			}
			if v.sent && (s.LossPct == nil || *s.LossPct != 100) {
				t.Fatal("no reply must be 100% loss")
			}
		})
	}
}
func TestV16PingMeanAndLastSuccessRemainEvidenceBased(t *testing.T) {
	p := NewPinger()
	now := time.Now()
	p.record("127.0.0.1", now, 10*time.Millisecond, true, nil)
	p.record("127.0.0.1", now.Add(time.Second), 30*time.Millisecond, true, nil)
	p.record("127.0.0.1", now.Add(2*time.Second), 0, true, context.DeadlineExceeded)
	s, c := p.Summary(now.Add(2*time.Second), time.Minute, "127.0.0.1")
	if s.MeanMS == nil || *s.MeanMS != 20 || c.LastSuccess == nil || !c.LastSuccess.Equal(now.Add(time.Second)) {
		t.Fatalf("%+v %+v", s, c)
	}
}

// Opt-in real kernel echo. It never contacts a public address.
func TestV16LoopbackPing(t *testing.T) {
	if os.Getenv("MONIK_NATIVE_PING") != "1" {
		t.Skip("opt-in kernel ICMP test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	rtt, sent, err := pingOnce(ctx, "127.0.0.1", time.Second)
	if err != nil || !sent || rtt < 0 {
		t.Fatalf("echo sent=%v rtt=%v err=%v", sent, rtt, err)
	}
}
