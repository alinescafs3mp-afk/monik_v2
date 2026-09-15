//go:build linux

package server

import (
	"context"
	"github.com/alinescafs3mp-afk/monik_v2/internal/agent/discovery"
	"github.com/alinescafs3mp-afk/monik_v2/internal/clock"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"net"
	"strconv"
	"testing"
	"time"
)

// Uses an actual kernel listener and temporary SQLite, with a fake clock solely
// for absence grace. It never inspects payloads on other ports or dials a host.
func TestV14KernelListenerCloseAndReappearPreservesIdentity(t *testing.T) {
	a, _ := testApp(t)
	clk := clock.NewFake(time.Now().UTC())
	a.Clock = clk
	a.Store.Clock = clk
	v14Register(t, a, "kernel")
	listener, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	target := listener.Addr().String()
	_, portText, _ := net.SplitHostPort(target)
	port, _ := strconv.Atoi(portText)
	scan := func(seq int64, expected bool) {
		t.Helper()
		all, e := discovery.Listeners()
		if e != nil {
			t.Fatal(e)
		}
		var ls []discovery.Listener
		for _, l := range all {
			if l.Port == port && l.IP.Equal(net.ParseIP("127.0.0.1")) {
				ls = append(ls, l)
			}
		}
		if (len(ls) > 0) != expected {
			t.Fatalf("actual kernel listener expected=%v, rows=%v", expected, ls)
		}
		d := discovery.InspectListenersPolicy(context.Background(), ls, nil, 0, nil, nil, false)
		if seq == 1 {
			d.Confirmed = []protocol.DiscoveredEndpoint{{ServiceID: "kernel-svc", DialTarget: target, URL: "http://" + target, Source: "listener+http", SpeaksHTTP: true}}
		}
		d.StartedAt = clk.Now().Add(-time.Millisecond)
		d.EndedAt = clk.Now()
		r := protocol.AgentReport{SchemaVersion: 3, AgentID: "kernel", SessionID: "s", Sequence: seq, ObservedAt: clk.Now(), IsLive: true, Discovery: d}
		if _, e = a.Store.AcceptReport(r); e != nil {
			t.Fatal(e)
		}
	}
	scan(1, true)
	initial, err := a.Store.Services("kernel")
	if err != nil || len(initial) != 1 {
		t.Fatal(initial, err)
	}
	originalID := initial[0].ID
	if v14Visible(t, a, false) != 1 {
		t.Fatal("missing initial service")
	}
	listener.Close()
	clk.Advance(time.Minute)
	scan(2, false)
	if v14Visible(t, a, false) != 1 {
		t.Fatal("single miss hid service")
	}
	clk.Advance(time.Minute)
	scan(3, false)
	if v14Visible(t, a, false) != 0 || v14Visible(t, a, true) != 1 {
		t.Fatal("absence did not retire presentation only")
	}
	listener, e = net.Listen("tcp4", target)
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	clk.Advance(time.Minute)
	scan(4, true)
	if v14Visible(t, a, false) != 1 {
		t.Fatal("returning listener did not revive original identity")
	}
	rows, e := a.Store.Services("kernel")
	if e != nil || len(rows) != 1 || rows[0].ID != originalID {
		t.Fatal(rows, e)
	}
}
