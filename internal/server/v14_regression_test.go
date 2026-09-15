package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/clock"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"github.com/alinescafs3mp-afk/monik_v2/internal/secure"
	"github.com/alinescafs3mp-afk/monik_v2/internal/storage"
)

// These regressions also compile on b969c6a; additive wire fields are built as
// JSON so a baseline failure proves behavior, not the lack of a new Go symbol.
func v14Register(t *testing.T, a *App, id string) {
	t.Helper()
	cfg := protocol.DefaultAgentConfig()
	b, _ := json.Marshal(cfg)
	if e := a.Store.InsertAgent(&storage.AgentRow{ID: id, Hostname: id, DesiredRevision: 1, DesiredHash: secure.SHA256Bytes(b), DesiredConfig: string(b)}, secure.HashToken("fixture")); e != nil {
		t.Fatal(e)
	}
}
func v14WireReport(t *testing.T, agent string, seq int64, now time.Time, targets []string, complete bool, eps []protocol.DiscoveredEndpoint) protocol.AgentReport {
	t.Helper()
	b, e := json.Marshal(map[string]any{"schema_version": 3, "agent_id": agent, "session_id": "inventory-test", "sequence": seq, "observed_at": now, "is_live": true,
		"discovery": map[string]any{"kind": "snapshot", "inventory_version": 1, "listener_coverage_complete": complete, "listener_targets": targets, "listener_count": len(targets), "coverage_complete": true, "started_at": now.Add(-time.Millisecond), "ended_at": now, "confirmed": eps}})
	if e != nil {
		t.Fatal(e)
	}
	var r protocol.AgentReport
	if e = json.Unmarshal(b, &r); e != nil {
		t.Fatal(e)
	}
	return r
}
func v14Visible(t *testing.T, a *App, all bool) int {
	t.Helper()
	url := "/api/v1/services"
	if all {
		url += "?inventory=all"
	}
	w := httptest.NewRecorder()
	a.handleServices(w, httptest.NewRequest("GET", url, nil), nil)
	if w.Code != 200 {
		t.Fatalf("%d: %s", w.Code, w.Body.String())
	}
	var result struct {
		Services []json.RawMessage `json:"services"`
	}
	if e := json.Unmarshal(w.Body.Bytes(), &result); e != nil {
		t.Fatal(e)
	}
	return len(result.Services)
}
func TestV14RegressMissingUnmonitoredServiceLeavesCurrentList(t *testing.T) {
	a, _ := testApp(t)
	clk := clock.NewFake(time.Now().UTC())
	a.Clock = clk
	a.Store.Clock = clk
	v14Register(t, a, "h")
	initial := v14WireReport(t, "h", 1, clk.Now(), []string{"127.0.0.1:9001"}, true, []protocol.DiscoveredEndpoint{{ServiceID: "ephemeral", URL: "http://127.0.0.1:9001", DialTarget: "127.0.0.1:9001", Source: "listener+http", SpeaksHTTP: true}})
	if _, e := a.Store.AcceptReport(initial); e != nil {
		t.Fatal(e)
	}
	clk.Advance(time.Minute)
	if _, e := a.Store.AcceptReport(v14WireReport(t, "h", 2, clk.Now(), nil, true, nil)); e != nil {
		t.Fatal(e)
	}
	if n := v14Visible(t, a, false); n != 1 {
		t.Fatalf("first absence hid service: %d", n)
	}
	clk.Advance(time.Minute)
	if _, e := a.Store.AcceptReport(v14WireReport(t, "h", 3, clk.Now(), nil, true, nil)); e != nil {
		t.Fatal(e)
	}
	if n := v14Visible(t, a, false); n != 0 {
		t.Fatalf("expired listener still in current inventory: %d", n)
	}
	if n := v14Visible(t, a, true); n != 1 {
		t.Fatalf("history/identity lost: %d", n)
	}
}
func TestV14RegressServerURLHostParsing(t *testing.T) {
	for raw, want := range map[string]string{"https://46.150.103.61:8777/": "46.150.103.61", "https://[::1]:8777/": "::1", "https://monik.example:8777/": "monik.example"} {
		if got := hostOf(raw); got != want {
			t.Errorf("hostOf(%q)=%q, want %q", raw, got, want)
		}
	}
}
func TestV14RegressOneDiscoveryOneDesiredRevision(t *testing.T) {
	a, h := testApp(t)
	v14Register(t, a, "h")
	r := v14WireReport(t, "h", 1, time.Now().UTC(), []string{"127.0.0.1:9001", "127.0.0.1:9002"}, true, []protocol.DiscoveredEndpoint{
		{ServiceID: "a", URL: "http://127.0.0.1:9001", DialTarget: "127.0.0.1:9001", Source: "listener+http", SpeaksHTTP: true},
		{ServiceID: "b", URL: "http://127.0.0.1:9002", DialTarget: "127.0.0.1:9002", Source: "listener+http", SpeaksHTTP: true}})
	raw, _ := json.Marshal(r)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agent/report", bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer fixture")
	req.Header.Set("X-Monik-Agent-Id", "h")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	ag, e := a.Store.Agent("h")
	if e != nil {
		t.Fatal(e)
	}
	if ag.DesiredRevision != 2 {
		t.Fatalf("one inventory created multiple config revisions: got %d, want 2", ag.DesiredRevision)
	}
	var cfg protocol.AgentConfig
	json.Unmarshal([]byte(ag.DesiredConfig), &cfg)
	if len(cfg.Checks) != 2 {
		t.Fatal(cfg)
	}
	for _, c := range cfg.Checks {
		if !c.Paused {
			t.Fatal("new discovery started monitoring automatically")
		}
	}
}
