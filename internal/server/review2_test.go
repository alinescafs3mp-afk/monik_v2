package server

import (
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"github.com/alinescafs3mp-afk/monik_v2/internal/storage"
	"net/http/httptest"
	"testing"
	"time"
)

func TestReviewPinIsPresentationOnly(t *testing.T) {
	a, _ := testApp(t)
	if e := a.Store.InsertAgent(&storage.AgentRow{ID: "pin", DesiredConfig: "{}", DesiredRevision: 7}, "test"); e != nil {
		t.Fatal(e)
	}
	for _, v := range []bool{true, false} {
		if e := a.applyPreference(protocol.SubmitOperation{Action: "agent.pin", Params: map[string]any{"agent_id": "pin", "pinned": v}}); e != nil {
			t.Fatal(e)
		}
		r, e := a.Store.Agent("pin")
		if e != nil || r.Pinned != v || r.DesiredRevision != 7 || r.DesiredConfig != "{}" {
			t.Fatal("presentation changed monitoring config")
		}
	}
	if e := a.applyPreference(protocol.SubmitOperation{Action: "agent.pin", Params: map[string]any{"agent_id": "pin", "pinned": "yes"}}); e == nil {
		t.Fatal("invalid checkbox value ignored")
	}
}
func TestReviewMalformedCheckCannotAppearApplied(t *testing.T) {
	cfg := protocol.DefaultAgentConfig()
	for _, p := range []map[string]any{{"check": map[string]any{"id": "c", "service_id": "s", "expected_status": []any{200.5}}}, {"check": map[string]any{}}, {"collect_seconds": 5.9}} {
		if e := applyConfigPatch(&cfg, protocol.SubmitOperation{Action: "check.apply", Params: p}); e == nil {
			t.Fatalf("accepted malformed check %+v", p)
		}
	}
}
func TestReviewHistoryDatabaseErrorIsNotMissingData(t *testing.T) {
	a, _ := testApp(t)
	a.Store.Close()
	w := httptest.NewRecorder()
	a.handleHistoryPoint(w, httptest.NewRequest("GET", "/api/v1/history/point?at="+time.Now().UTC().Format(time.RFC3339)+"&agent_id=a", nil), &storage.Session{})
	if w.Code != 500 {
		t.Fatalf("db failure hidden as empty history: %d %s", w.Code, w.Body.String())
	}
}
func TestReviewUnimplementedActionsDoNotClaimSuccess(t *testing.T) {
	a, _ := testApp(t)
	for _, action := range []string{"operation.retry_selected", "update.resume"} {
		w := httptest.NewRecorder()
		a.processSubmit(w, &storage.Session{Username: "owner"}, protocol.SubmitOperation{Action: action, ClientRequestKey: action, Params: map[string]any{}})
		if w.Code != 501 {
			t.Fatalf("%s %d", action, w.Code)
		}
	}
}

// Real implementations reject empty requests before journaling any parameters.
func TestAudit6ImplementedPoliciesRejectEmptyRequests(t *testing.T) {
	a, _ := testApp(t)
	for _, action := range []string{"rule.save", "maintenance.set"} {
		w := httptest.NewRecorder()
		a.processSubmit(w, &storage.Session{Username: "owner", Role: "owner"}, protocol.SubmitOperation{Action: action, ClientRequestKey: action, Params: map[string]any{}})
		if w.Code != 400 {
			t.Fatal(action, w.Code, w.Body.String())
		}
		var n int
		if e := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM operations WHERE client_request_key=?`, action).Scan(&n); e != nil || n != 0 {
			t.Fatal(n, e)
		}
	}
}
