package storage

import (
	"encoding/json"
	"errors"
	"github.com/alinescafs3mp-afk/monik_v2/internal/clock"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"github.com/alinescafs3mp-afk/monik_v2/internal/secure"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func audit5Store(t *testing.T) (*Store, *clock.Fake) {
	t.Helper()
	clk := clock.NewFake(time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC))
	s, e := Open(filepath.Join(t.TempDir(), "test.db"), clk)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	return s, clk
}
func announceReq(id string) protocol.Announcement {
	return protocol.Announcement{AgentID: id, Credential: strings.Repeat("ab", 32), Hostname: "host", DisplayName: "Мой сервер", OS: "linux", Arch: "amd64", Version: "fixture"}
}
func TestAudit5PendingEnrollmentRequiresApprovalAndSameProof(t *testing.T) {
	s, _ := audit5Store(t)
	r := announceReq("candidate-1")
	state, created, e := s.Announce(r, "127.0.0.1")
	if e != nil || state != "pending" || !created {
		t.Fatalf("%s %v %v", state, created, e)
	}
	if _, e = s.Agent(r.AgentID); !errors.Is(e, ErrNotFound) {
		t.Fatal("unapproved candidate became a monitoring agent")
	}
	rows, e := s.AgentCandidates()
	if e != nil || len(rows) != 1 {
		t.Fatal(rows, e)
	}
	raw, _ := json.Marshal(rows)
	if strings.Contains(string(raw), r.Credential) || strings.Contains(string(raw), secure.HashToken(r.Credential)) {
		t.Fatal("registration proof/verifier leaked")
	}
	bad := r
	bad.Credential = strings.Repeat("cd", 32)
	if _, _, e = s.Announce(bad, "127.0.0.1"); e == nil {
		t.Fatal("different proof hijacked candidate")
	}
	fp := protocol.RegistrationFingerprint(r.AgentID, r.Credential)
	if e = s.DecideCandidate(r.AgentID, strings.Repeat("0", 64), "owner", true); e == nil {
		t.Fatal("wrong fingerprint approved")
	}
	for i := 0; i < 2; i++ {
		if e = s.DecideCandidate(r.AgentID, fp, "owner", true); e != nil {
			t.Fatal(e)
		}
	}
	if e = s.DecideCandidate(r.AgentID, fp, "owner", false); e == nil {
		t.Fatal("opposite decision silently rewrote approval")
	}
	ag, e := s.Agent(r.AgentID)
	if e != nil || ag.Pinned || ag.LastLiveAt != nil {
		t.Fatal("approval fabricated a pin/live heartbeat", ag, e)
	}
	var n int
	if e = s.DB.QueryRow("SELECT COUNT(*) FROM config_revisions WHERE agent_id=?", r.AgentID).Scan(&n); e != nil || n != 1 {
		t.Fatal(n, e)
	}
	if state, _, e = s.Announce(r, "127.0.0.1"); e != nil || state != "approved" {
		t.Fatal(state, e)
	}
	if e = s.RevokeAgent(r.AgentID); e != nil {
		t.Fatal(e)
	}
	if _, _, e = s.Announce(r, "127.0.0.1"); e == nil {
		t.Fatal("revoked candidate silently reenrolled")
	}
}
func TestAudit5StaleRejectedAndExpiredCandidate(t *testing.T) {
	s, clk := audit5Store(t)
	r := announceReq("candidate-stale")
	fp := protocol.RegistrationFingerprint(r.AgentID, r.Credential)
	if _, _, e := s.Announce(r, "127.0.0.1"); e != nil {
		t.Fatal(e)
	}
	clk.Advance(3 * time.Minute)
	if e := s.DecideCandidate(r.AgentID, fp, "owner", true); e == nil {
		t.Fatal("stale candidate approved")
	}
	if _, _, e := s.Announce(r, "127.0.0.1"); e != nil {
		t.Fatal(e)
	}
	if e := s.DecideCandidate(r.AgentID, fp, "owner", false); e != nil {
		t.Fatal(e)
	}
	if state, _, e := s.Announce(r, "127.0.0.1"); e != nil || state != "rejected" {
		t.Fatal(state, e)
	}
	if rows, e := s.AgentCandidates(); e != nil || len(rows) != 0 {
		t.Fatal(rows, e)
	}
	clk.Advance(8 * 24 * time.Hour)
	if state, created, e := s.Announce(r, "127.0.0.1"); e != nil || state != "pending" || !created {
		t.Fatal(state, created, e)
	}
}
func TestAudit5NameSurvivesTelemetryAndCompareAndSwap(t *testing.T) {
	s, _ := audit5Store(t)
	r := announceReq("named")
	_, _, e := s.Announce(r, "127.0.0.1")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.DecideCandidate(r.AgentID, protocol.RegistrationFingerprint(r.AgentID, r.Credential), "owner", true); e != nil {
		t.Fatal(e)
	}
	old := r.DisplayName
	if e = s.RenameAgent(r.AgentID, "Сервер / новый", &old); e != nil {
		t.Fatal(e)
	}
	if e = s.TouchAgent(r.AgentID, "boot", 1, true, &protocol.HostMetrics{Hostname: "host-updated", DisplayName: "old worker label"}, nil, nil); e != nil {
		t.Fatal(e)
	}
	ag, e := s.Agent(r.AgentID)
	if e != nil || ag.DisplayName != "Сервер / новый" || ag.Hostname != "host-updated" {
		t.Fatalf("rename lost after telemetry: %+v %v", ag, e)
	}
	if e = s.RenameAgent(r.AgentID, "stale browser overwrite", &old); !errors.Is(e, ErrConflict) {
		t.Fatal("stale form overwrote new name", e)
	}
}
func TestAudit5AcknowledgementIsIdempotentNotRecovery(t *testing.T) {
	s, clk := audit5Store(t)
	if e := s.InsertIncident(map[string]any{"id": "incident", "entity_type": "agent", "entity_id": "a", "metric": "cpu", "severity": "critical", "status": "confirmed", "reason": "load"}); e != nil {
		t.Fatal(e)
	}
	if e := s.AckIncident("incident", "first-owner"); e != nil {
		t.Fatal(e)
	}
	rows, e := s.OpenIncidents()
	if e != nil || len(rows) != 1 {
		t.Fatal(rows, e)
	}
	at := rows[0]["acked_at"]
	clk.Advance(time.Hour)
	if e = s.AckIncident("incident", "another-owner"); e != nil {
		t.Fatal(e)
	}
	rows, e = s.OpenIncidents()
	if e != nil || rows[0]["acked_at"] != at || rows[0]["status"] != "confirmed" {
		t.Fatal(rows, e)
	}
	var actor string
	_ = s.DB.QueryRow("SELECT acked_by FROM incidents WHERE id='incident'").Scan(&actor)
	if actor != "first-owner" {
		t.Fatal(actor)
	}
}
