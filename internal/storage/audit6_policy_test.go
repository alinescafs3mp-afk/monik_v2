package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/clock"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"github.com/alinescafs3mp-afk/monik_v2/internal/rules"
	"github.com/alinescafs3mp-afk/monik_v2/internal/secure"
)

func TestAudit6AdmissionClosedExpiryCASAndExistingCandidates(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC))
	path := filepath.Join(t.TempDir(), "state.db")
	s, e := Open(path, clk)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { s.Close() }()
	policy, e := s.AdmissionPolicy()
	if e != nil || policy.Open || policy.Revision != 0 {
		t.Fatal(policy, e)
	}
	req := announceReq("candidate-window")
	if state, created, e := s.Announce(req, "127.0.0.1"); e != nil || state != "admission_closed" || created {
		t.Fatal(state, created, e)
	}
	policy, e = s.SetAdmission(0, 1, "owner")
	if e != nil || !policy.Open {
		t.Fatal(policy, e)
	}
	if _, e = s.SetAdmission(0, 60, "stale-tab"); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	if state, _, e := s.Announce(req, "127.0.0.1"); e != nil || state != "pending" {
		t.Fatal(state, e)
	}
	clk.Advance(time.Minute)
	if state, _, e := s.Announce(announceReq("unknown"), "127.0.0.1"); e != nil || state != "admission_closed" {
		t.Fatal(state, e)
	}
	if state, _, e := s.Announce(req, "127.0.0.1"); e != nil || state != "pending" {
		t.Fatal("existing pending stranded", state, e)
	}
	if e = s.DecideCandidate(req.AgentID, protocol.RegistrationFingerprint(req.AgentID, req.Credential), "owner", true); e != nil {
		t.Fatal(e)
	}
	s.Close()
	s, e = Open(path, clk)
	if e != nil {
		t.Fatal(e)
	}
	if state, _, e := s.Announce(req, "127.0.0.1"); e != nil || state != "approved" {
		t.Fatal("approved proof stranded", state, e)
	}
	if e = s.SetSetting("admission_window_v1", `{"invalid":`); e != nil {
		t.Fatal(e)
	}
	if _, _, e = s.Announce(announceReq("corrupt-policy"), "127.0.0.1"); e == nil {
		t.Fatal("corrupt policy accepted unknown identity")
	}
}
func TestAudit6RuleVersionsAndPolicyChangeNotRecovery(t *testing.T) {
	s, clk := audit5Store(t)
	before := clk.Now()
	initial, e := s.HostRulesAt(before)
	if e != nil || initial.Revision != 0 {
		t.Fatal(initial, e)
	}
	p := IncidentPolicy{Failures: 1, Successes: 1, MaxGap: time.Minute}
	if e = s.ObserveIncident("agent", "h", "cpu", before, true, true, "critical", "before", p); e != nil {
		t.Fatal(e)
	}
	list := rules.DefaultThresholds()
	list[0].Warning = 90
	clk.Advance(time.Second)
	saved, e := s.SaveHostRules(0, list, "owner")
	if e != nil || saved.Revision <= 0 {
		t.Fatal(saved, e)
	}
	old, e := s.HostRulesAt(before)
	if e != nil || old.Revision != 0 || old.Rules[0].Warning != 85 {
		t.Fatal("past rewritten", old, e)
	}
	current, e := s.HostRulesAt(clk.Now())
	if e != nil || current.Revision != saved.Revision || current.Rules[0].Warning != 90 {
		t.Fatal(current, e)
	}
	if _, e = s.SaveHostRules(0, list, "stale-tab"); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	rows, _, e := s.SearchIncidents(context.Background(), IncidentQuery{From: before.Add(-time.Minute), To: clk.Now().Add(time.Minute), State: "policy_changed"})
	if e != nil || len(rows) != 1 || !strings.Contains(rows[0]["reason"].(string), "not observed recovery") {
		t.Fatal(rows, e)
	}
	if open, e := s.OpenIncidents(); e != nil || len(open) != 0 {
		t.Fatal(open, e)
	}
	bad := rules.DefaultThresholds()
	bad[0].Recovery = 99
	if _, e = s.SaveHostRules(saved.Revision, bad, "owner"); e == nil {
		t.Fatal("invalid threshold accepted")
	}
	if _, e = s.DB.Exec(`UPDATE rule_versions SET body='null' WHERE id=?`, saved.Revision); e != nil {
		t.Fatal(e)
	}
	if _, e = s.HostRulesAt(clk.Now()); e == nil {
		t.Fatal("corrupt rules accepted")
	}
}
func TestAudit6MaintenanceScopeCancelAndBoundaries(t *testing.T) {
	s, clk := audit5Store(t)
	for _, id := range []string{"h", "other"} {
		if e := s.InsertAgent(&AgentRow{ID: id, Hostname: id, DisplayName: id}, "hash"); e != nil {
			t.Fatal(e)
		}
	}
	if e := s.UpsertService(protocol.DiscoveredEndpoint{ServiceID: "svc", DialTarget: "127.0.0.1:8000", URL: "http://127.0.0.1:8000"}, "h"); e != nil {
		t.Fatal(e)
	}
	start := clk.Now().Add(time.Minute)
	end := start.Add(time.Hour)
	m := MaintenanceWindow{ID: "window", EntityType: "agent", EntityID: "h", Purpose: "Заменить диск", StartAt: start, EndAt: end, CreatedBy: "owner"}
	if e := s.CreateMaintenance(m); e != nil {
		t.Fatal(e)
	}
	for _, v := range []struct {
		kind, id string
		at       time.Time
		want     bool
	}{{"agent", "h", start.Add(-time.Nanosecond), false}, {"agent", "h", start, true}, {"service", "svc", start, true}, {"agent", "other", start, false}, {"agent", "h", end, false}} {
		yes, e := s.InMaintenance(v.kind, v.id, v.at)
		if e != nil || yes != v.want {
			t.Fatal(v, yes, e)
		}
	}
	clk.Advance(2 * time.Minute)
	cancelAt := clk.Now()
	if e := s.CancelMaintenance("window", "owner"); e != nil {
		t.Fatal(e)
	}
	clk.Advance(time.Minute)
	if e := s.CancelMaintenance("window", "again"); e != nil {
		t.Fatal(e)
	}
	for _, at := range []time.Time{start, cancelAt.Add(-time.Nanosecond)} {
		yes, e := s.InMaintenance("service", "svc", at)
		if e != nil || !yes {
			t.Fatal("historical interval erased", e)
		}
	}
	yes, e := s.InMaintenance("service", "svc", cancelAt)
	if e != nil || yes {
		t.Fatal("cancelled maintenance active", e)
	}
	windows, truncated, e := s.MaintenanceWindows()
	if e != nil || len(windows) != 1 || truncated || windows[0].CancelledBy != "owner" || !windows[0].CancelledAt.Equal(cancelAt) {
		t.Fatal(windows, truncated, e)
	}
	if e = s.CancelMaintenance("missing", "owner"); e == nil {
		t.Fatal("missing cancellation succeeded")
	}
	m.ID = "bad"
	m.EndAt = start.Add(8 * 24 * time.Hour)
	if e = s.CreateMaintenance(m); e == nil {
		t.Fatal("unbounded maintenance")
	}
}
func TestAudit6UnreadFilterUnackAndPeakSeverity(t *testing.T) {
	s, clk := audit5Store(t)
	at := clk.Now()
	p := IncidentPolicy{Failures: 1, Successes: 1, MaxGap: time.Minute, Maintenance: true}
	if e := s.ObserveIncident("agent", "h", "cpu", at, true, true, "warning", "high", p); e != nil {
		t.Fatal(e)
	}
	id, e := s.FindOpenIncident("h", "cpu")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.AckIncident(id, "owner"); e != nil {
		t.Fatal(e)
	}
	query := IncidentQuery{From: at.Add(-time.Minute), To: at.Add(time.Hour), State: "open", Acknowledgement: "unread"}
	rows, _, e := s.SearchIncidents(context.Background(), query)
	if e != nil || len(rows) != 0 {
		t.Fatal(rows, e)
	}
	if e = s.UnackIncident(id, "owner"); e != nil {
		t.Fatal(e)
	}
	rows, _, e = s.SearchIncidents(context.Background(), query)
	if e != nil || len(rows) != 1 || rows[0]["maintenance_observed"] != true {
		t.Fatal(rows, e)
	}
	clk.Advance(5 * time.Second)
	if e = s.ObserveIncident("agent", "h", "cpu", clk.Now(), true, true, "critical", "critical", p); e != nil {
		t.Fatal(e)
	}
	if e = s.AckIncident(id, "owner"); e != nil {
		t.Fatal(e)
	}
	for _, severity := range []string{"warning", "critical"} {
		clk.Advance(5 * time.Second)
		if e = s.ObserveIncident("agent", "h", "cpu", clk.Now(), true, true, severity, "still failing", p); e != nil {
			t.Fatal(e)
		}
	}
	rows, e = s.OpenIncidents()
	if e != nil || rows[0]["acked_at"] == "" || rows[0]["severity"] != "critical" {
		t.Fatal(rows, e)
	}
	query.Acknowledgement = "banana"
	if _, _, e = s.SearchIncidents(context.Background(), query); !errors.Is(e, ErrInvalidHistoryQuery) {
		t.Fatal(e)
	}
}
func TestAudit6PasswordCASRevokesSessionsAndOldLogin(t *testing.T) {
	s, _ := audit5Store(t)
	hash, e := secure.HashPassword("old-password-fixture")
	if e != nil {
		t.Fatal(e)
	}
	user, e := s.CreateUser("owner", hash, "owner")
	if e != nil {
		t.Fatal(e)
	}
	token, session, e := s.CreateSession(user, time.Hour, time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	second, _, e := s.CreateSession(user, time.Hour, time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	newHash, e := secure.HashPassword("new-password-fixture")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.ChangePassword(user.ID, hash, newHash, "owner"); e != nil {
		t.Fatal(e)
	}
	for _, raw := range []string{token, second} {
		if _, e = s.SessionByToken(raw); !errors.Is(e, ErrNotFound) {
			t.Fatal("old session survived", e)
		}
	}
	if e = s.TouchRecentAuth(session.ID, time.Now().Add(time.Hour)); !errors.Is(e, ErrNotFound) {
		t.Fatal("revoked reauth succeeded", e)
	}
	if _, _, e = s.CreateSession(user, time.Hour, time.Minute); !errors.Is(e, ErrConflict) {
		t.Fatal("racing old login recreated session", e)
	}
	if e = s.ChangePassword(user.ID, hash, "wrong", "owner"); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	current, e := s.UserByName("owner")
	if e != nil || !secure.VerifyPassword(current.PasswordHash, "new-password-fixture") {
		t.Fatal(e)
	}
	if _, _, e = s.CreateSession(current, time.Hour, time.Minute); e != nil {
		t.Fatal(e)
	}
	var detail string
	if e = s.DB.QueryRow(`SELECT detail FROM audit_events WHERE action='account.password.change'`).Scan(&detail); e != nil {
		t.Fatal(e)
	}
	if strings.Contains(detail, "fixture") || strings.Contains(detail, newHash) {
		t.Fatal("password leaked in audit")
	}
}
func TestAudit6RetentionIsBoundedAndDiagnosticsAreReal(t *testing.T) {
	s, clk := audit5Store(t)
	old := clk.Now().Add(-72 * time.Hour).Format(dbTimeFormat)
	tx, e := s.DB.Begin()
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 10500; i++ {
		if _, e = tx.Exec(`INSERT INTO host_samples(agent_id,observed_at,received_at,payload) VALUES('h',?,?, '{}')`, old, old); e != nil {
			t.Fatal(e)
		}
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	if e = s.RetainRaw(48 * time.Hour); e != nil {
		t.Fatal(e)
	}
	var n int
	if e = s.DB.QueryRow(`SELECT COUNT(*) FROM host_samples`).Scan(&n); e != nil || n != 500 {
		t.Fatal(n, e)
	}
	d, e := s.StorageDiagnostics(48 * time.Hour)
	if e != nil || d["synchronous"] != 2 {
		t.Fatal(d, e)
	}
	if d["host_samples"].(map[string]any)["retention_lag_seconds"].(float64) <= 0 {
		t.Fatal("cleanup lag concealed")
	}
	if e = s.RetainRaw(48 * time.Hour); e != nil {
		t.Fatal(e)
	}
	if e = s.RetainRaw(0); e == nil {
		t.Fatal("zero retention accepted")
	}
}
func TestAudit6NullObservationRejected(t *testing.T) {
	s, clk := audit5Store(t)
	if _, e := s.DB.Exec(`INSERT INTO service_observations(agent_id,service_id,observed_at,received_at,payload) VALUES('h','s',?,?,'null')`, clk.Now().Format(dbTimeFormat), clk.Now().Format(dbTimeFormat)); e != nil {
		t.Fatal(e)
	}
	if _, e := s.LatestCheckObs("s"); e == nil {
		t.Fatal("null accepted")
	}
	if _, e := s.CheckSeries("s", clk.Now().Add(-time.Second), clk.Now().Add(time.Second)); e == nil {
		t.Fatal("null accepted")
	}
}
func TestAudit6RulesValidation(t *testing.T) {
	for _, change := range []func([]rules.HostThreshold){func(r []rules.HostThreshold) { r[0].Warning = 100 }, func(r []rules.HostThreshold) { r[1].Metric = "cpu" }, func(r []rules.HostThreshold) { r[0].PersistSeconds = 0 }, func(r []rules.HostThreshold) { r[0].Recovery = -1 }} {
		list := rules.DefaultThresholds()
		change(list)
		if e := rules.ValidateThresholds(list); e == nil {
			raw, _ := json.Marshal(list)
			t.Fatal(string(raw))
		}
	}
}

// Completed short windows must not push a long active window out of the bounded list.
func TestAudit6MaintenanceListKeepsActionableWindowsFirst(t *testing.T) {
	s, clk := audit5Store(t)
	now := clk.Now()
	stamp := func(t time.Time) string { return t.UTC().Format(dbTimeFormat) }
	if _, err := s.DB.Exec(`INSERT INTO maintenance_windows(id,entity_type,entity_id,purpose,start_at,end_at,created_by) VALUES('active','fleet','','ongoing',?,?,'owner')`, stamp(now.Add(-6*24*time.Hour)), stamp(now.Add(time.Hour))); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 201; i++ {
		if _, err := s.DB.Exec(`INSERT INTO maintenance_windows(id,entity_type,entity_id,purpose,start_at,end_at,created_by) VALUES(?,'fleet','','finished',?,?,'owner')`, fmt.Sprint("ended-", i), stamp(now.Add(-2*time.Hour)), stamp(now.Add(-time.Hour))); err != nil {
			t.Fatal(err)
		}
	}
	rows, truncated, err := s.MaintenanceWindows()
	if err != nil || !truncated || len(rows) != 200 || rows[0].ID != "active" {
		t.Fatal(len(rows), truncated, err)
	}
}
