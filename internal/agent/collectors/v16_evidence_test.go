package collectors

import (
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"testing"
	"time"
)

func TestV16UnsentPingCannotClaimSuccessfulCollection(t *testing.T) {
	now := time.Now()
	p := NewPinger()
	p.samples = []sample{{at: now, sent: false, ok: false}}
	out, cap := p.Summary(now, time.Minute, "8.8.8.8")
	if cap.Status == protocol.CapSupported || cap.LastSuccess != nil || out.MeanMS != nil || out.LossPct != nil {
		t.Fatalf("unsent packet looks successful: %+v %+v", out, cap)
	}
}
