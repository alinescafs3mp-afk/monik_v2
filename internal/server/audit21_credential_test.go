package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/secure"
	"github.com/alinescafs3mp-afk/monik_v2/internal/storage"
)

func TestV21RetiredKeyCannotStageCredentialAfterBodyWait(t *testing.T) {
	for _, change := range []string{"rotated", "revoked"} {
		t.Run(change, func(t *testing.T) {
			a, h := testApp(t)
			old, next := strings.Repeat("a", 64), strings.Repeat("b", 64)
			if err := a.Store.InsertAgent(&storage.AgentRow{ID: "v21-rotation"}, secure.HashToken(old)); err != nil {
				t.Fatal(err)
			}
			gate := &v20GateBody{Reader: strings.NewReader(`{"job_id":"rotation-fixture","verifier":"` + strings.Repeat("c", 64) + `"}`), entered: make(chan struct{}), resume: make(chan struct{})}
			r := httptest.NewRequest("POST", "https://localhost/api/v1/agent/credential/next", nil)
			r.Body = gate
			r.Header.Set("Authorization", "Bearer "+old)
			r.Header.Set("X-Monik-Agent-Id", "v21-rotation")
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			done := make(chan struct{})
			go func() { h.ServeHTTP(w, r); close(done) }()
			select {
			case <-gate.entered:
			case <-time.After(5 * time.Second):
				close(gate.resume)
				t.Fatal("body gate not reached")
			}
			var err error
			if change == "revoked" {
				_, err = a.Store.DB.Exec(`UPDATE agents SET revoked=1 WHERE id='v21-rotation'`)
			} else {
				_, err = a.Store.DB.Exec(`UPDATE agents SET credential_hash=? WHERE id='v21-rotation'`, secure.HashToken(next))
			}
			close(gate.resume)
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("request did not finish")
			}
			if w.Code != 401 && w.Code != 409 {
				t.Fatalf("retired credential registered a successor: %d %s", w.Code, w.Body.String())
			}
			var n int
			if err = a.Store.DB.QueryRow(`SELECT count(*) FROM agent_credential_overlap WHERE agent_id='v21-rotation'`).Scan(&n); err != nil || n != 0 {
				t.Fatalf("rejected request left pending credential: %d %v", n, err)
			}
		})
	}
}

func TestV21CredentialRegistrationRejectsMalformedVerifier(t *testing.T) {
	for _, v := range []struct{ job, hash string }{{"rotation", strings.Repeat("z", 64)}, {"", strings.Repeat("c", 64)}} {
		t.Run(v.job+v.hash[:1], func(t *testing.T) {
			a, h := testApp(t)
			token := strings.Repeat("a", 64)
			if err := a.Store.InsertAgent(&storage.AgentRow{ID: "v21-rotation"}, secure.HashToken(token)); err != nil {
				t.Fatal(err)
			}
			b, _ := json.Marshal(map[string]string{"job_id": v.job, "verifier": v.hash})
			r := httptest.NewRequest("POST", "https://localhost/api/v1/agent/credential/next", strings.NewReader(string(b)))
			r.Header.Set("Authorization", "Bearer "+token)
			r.Header.Set("X-Monik-Agent-Id", "v21-rotation")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("malformed candidate accepted: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestV21NormalCredentialRegistrationAndPromotionStillWork(t *testing.T) {
	a, h := testApp(t)
	old, next := strings.Repeat("a", 64), strings.Repeat("b", 64)
	if err := a.Store.InsertAgent(&storage.AgentRow{ID: "v21-rotation"}, secure.HashToken(old)); err != nil {
		t.Fatal(err)
	}
	request := func(token string) *httptest.ResponseRecorder {
		b, _ := json.Marshal(map[string]string{"job_id": "rotation-fixture", "verifier": secure.HashToken(next)})
		r := httptest.NewRequest("POST", "https://localhost/api/v1/agent/credential/next", strings.NewReader(string(b)))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("X-Monik-Agent-Id", "v21-rotation")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	for i := 0; i < 2; i++ {
		if w := request(old); w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	r := httptest.NewRequest("GET", "https://localhost/", nil)
	r.Header.Set("Authorization", "Bearer "+next)
	r.Header.Set("X-Monik-Agent-Id", "v21-rotation")
	if _, _, ok := a.agentFrom(r); !ok {
		t.Fatal("valid staged token did not promote")
	}
	if w := request(next); w.Code != 200 || !strings.Contains(w.Body.String(), `"already_current":true`) {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := request(old); w.Code != 401 {
		t.Fatal("old credential still admitted", w.Code)
	}
}
