//go:build linux

package server

import (
	"context"
	ac "github.com/alinescafs3mp-afk/monik_v2/internal/agentconsole"
	"net"
	"strings"
	"testing"
	"time"
)

// This runs the complete TLS browser/relay/agent path against a kernel PTY.
// UID separation is tested separately by the activated-peer native suite.
func TestV18TLSRelayUsesRealPTYAndTerminalResize(t *testing.T) {
	f := v18New(t)
	t.Setenv("HOME", t.TempDir())
	f.startWith(t, func(ctx context.Context) (net.Conn, error) {
		a, b := net.Pipe()
		f.localWG.Add(1)
		go func() { defer f.localWG.Done(); defer b.Close(); _ = ac.ServeLocal(ctx, b, "monik-console") }()
		return a, nil
	})
	s := f.open(t, f.ticket(t))
	v18Ready(t, s)
	if e := s.Send(ac.Message{Type: "resize", Seq: 1, Cols: 101, Rows: 31}); e != nil {
		t.Fatal(e)
	}
	if e := s.Send(ac.Message{Type: "input", Seq: 2, Data: "printf '\\nWIRE_%s\\n' real; stty size\n"}); e != nil {
		t.Fatal(e)
	}
	output := ""
	s.WS.SetReadDeadline(time.Now().Add(5 * time.Second))
	for !strings.Contains(output, "WIRE_real") || !strings.Contains(output, "31 101") {
		m, e := s.Read()
		if e != nil {
			t.Fatal(e, output)
		}
		if m.Type == "output" {
			b, e := ac.OutputBytes(m)
			if e != nil {
				t.Fatal(e)
			}
			output += string(b)
		}
	}
	if len(output) > 5000 {
		t.Fatal("unexpected unbounded output")
	}
	_ = s.Close()
}
