package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"github.com/alinescafs3mp-afk/monik_v2/internal/storage"
)

func seedReadOperation(t *testing.T, a *App, id string) *protocol.Operation {
	t.Helper()
	op := &protocol.Operation{ID: id, Action: "preference.save", Status: protocol.OpCompletedWithErrs, Revision: 1, ClientRequestKey: id, Actor: "owner", CreatedAt: a.Clock.Now(), Params: map[string]any{}, Targets: []protocol.TargetResult{{AgentID: "server", Status: protocol.TargetFailed, Stage: "failed", Message: "test failure"}}}
	if e := a.Store.InsertOperation(op, "hash"); e != nil {
		t.Fatal(e)
	}
	op, e := a.Store.Operation(id)
	if e != nil {
		t.Fatal(e)
	}
	return op
}
func TestAudit9ReadHTTPSeparatesNoticeFromExecution(t *testing.T) {
	a, h := testApp(t)
	op := seedReadOperation(t, a, "http-read")
	call := audit6HTTP(t, a, h, -time.Minute) // Viewing/read marks do not require recent auth.
	body := map[string]any{"read": true, "targets": []storage.OperationReadTarget{{ID: op.ID, AttentionRevision: op.AttentionRevision}}}
	for i := 0; i < 2; i++ {
		w := call("POST", "/api/v1/operations/read", body)
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"saved":true`) {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	got, e := a.Store.Operation(op.ID)
	if e != nil || !got.Read || got.NeedsAttention || got.Status != protocol.OpCompletedWithErrs {
		t.Fatal(got, e)
	}
	w := call("GET", "/api/v1/operations/summary", nil)
	var counts storage.OperationCounts
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &counts) != nil || counts.Total != 1 || counts.UnreadAttention != 0 || counts.Attention != 1 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = call("GET", "/api/v1/overview", nil)
	var overview map[string]any
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &overview) != nil {
		t.Fatal(w.Code, w.Body.String())
	}
	if overview["operations_attention"] != float64(0) {
		t.Fatal(overview)
	}
	w = call("GET", "/api/v1/operations?filter=errors&read=read&q=http-read", nil)
	var page storage.OperationPage
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Operations) != 1 || !page.Operations[0].Read {
		t.Fatal(w.Code, w.Body.String())
	}
	body["read"] = false
	w = call("POST", "/api/v1/operations/read", body)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	w = call("GET", "/api/v1/operations?filter=attention", nil)
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Operations) != 1 || page.Operations[0].Read {
		t.Fatal(w.Code, w.Body.String())
	}
}
func TestAudit9ReadHTTPRejectsUnauthenticatedAndCSRF(t *testing.T) {
	a, h := testApp(t)
	seedReadOperation(t, a, "protected")
	raw := `{"read":true,"targets":[{"operation_id":"protected","attention_revision":1}]}`
	r := httptest.NewRequest("POST", "https://localhost/api/v1/operations/read", strings.NewReader(raw))
	r.RemoteAddr = "127.0.0.1:1234"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal(w.Code, w.Body.String())
	}
	user, _ := a.Store.UserByName("owner")
	token, _, e := a.Store.CreateSession(user, time.Hour, time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	r = httptest.NewRequest("POST", "https://localhost/api/v1/operations/read", strings.NewReader(raw))
	r.RemoteAddr = "127.0.0.1:1234"
	r.AddCookie(&http.Cookie{Name: "monik_session", Value: token})
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
	r = httptest.NewRequest("POST", "https://localhost/api/v1/operations/read", strings.NewReader(raw))
	w = httptest.NewRecorder()
	a.handleOperationReads(w, r, &storage.Session{Role: "viewer", Username: "viewer"})
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
	got, _ := a.Store.Operation("protected")
	if got.Read {
		t.Fatal("read accepted without authority")
	}
}
func TestAudit9ReadHTTPValidationConflictAndNoExtraJobs(t *testing.T) {
	a, h := testApp(t)
	call := audit6HTTP(t, a, h, time.Minute)
	op := seedReadOperation(t, a, "versioned")
	for _, body := range []any{map[string]any{"targets": []any{}}, map[string]any{"read": true, "targets": []any{}}, map[string]any{"read": "true"}, map[string]any{"read": true, "targets": []any{map[string]any{"operation_id": op.ID, "attention_revision": 0}}}, map[string]any{"read": true, "unknown": true}} {
		w := call("POST", "/api/v1/operations/read", body)
		if w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	target := storage.OperationReadTarget{ID: op.ID, AttentionRevision: op.AttentionRevision}
	if e := a.Store.UpdateTarget(op.ID, "server", protocol.TargetFailed, "changed", "changed", "changed", false, nil); e != nil {
		t.Fatal(e)
	}
	w := call("POST", "/api/v1/operations/read", map[string]any{"read": true, "targets": []storage.OperationReadTarget{target}})
	if w.Code != 409 {
		t.Fatal(w.Code, w.Body.String())
	}
	target.ID = "missing"
	w = call("POST", "/api/v1/operations/read", map[string]any{"read": true, "targets": []storage.OperationReadTarget{target}})
	if w.Code != 404 {
		t.Fatal(w.Code)
	}
	var n int
	if e := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM operations`).Scan(&n); e != nil || n != 1 {
		t.Fatal(n, e)
	}
	if e := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM agent_jobs`).Scan(&n); e != nil || n != 0 {
		t.Fatal(n, e)
	}
}
func TestAudit9ReadHTTPMalformedEvidenceIsServerError(t *testing.T) {
	a, h := testApp(t)
	call := audit6HTTP(t, a, h, time.Minute)
	seedReadOperation(t, a, "broken")
	if _, e := a.Store.DB.Exec(`UPDATE operation_targets SET evidence='not-json' WHERE operation_id='broken'`); e != nil {
		t.Fatal(e)
	}
	for _, path := range []string{"/api/v1/operations/broken", "/api/v1/operations"} {
		w := call("GET", path, nil)
		if w.Code != 500 {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
	for _, path := range []string{"/api/v1/operations?limit=0", "/api/v1/operations?limit=201", "/api/v1/operations?filter=imaginary", "/api/v1/operations?read=maybe", "/api/v1/operations?before=invalid"} {
		w := call("GET", path, nil)
		if w.Code != 400 {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
}
