package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/clock"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"github.com/alinescafs3mp-afk/monik_v2/internal/storage"
)

func v21Request(method, path, token, csrf string) *http.Request {
	r := httptest.NewRequest(method, "https://localhost"+path, strings.NewReader(`{}`))
	r.AddCookie(&http.Cookie{Name: "monik_session", Value: token})
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-CSRF-Token", csrf)
	r.Header.Set("Origin", "https://localhost")
	return r
}

func TestV21ViewerCanRevokeOwnBrowserSession(t *testing.T) {
	a, h := testApp(t)
	token, s := v20Session(t, a, "viewer")
	ownerToken, _ := v20Session(t, a, "owner")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, v21Request("POST", "/api/v1/logout", token, s.CSRF))
	if w.Code != 200 {
		t.Fatalf("viewer cannot sign out: %d %s", w.Code, w.Body.String())
	}
	if _, e := a.Store.SessionByToken(token); e == nil {
		t.Fatal("sign out left viewer token usable")
	}
	if _, e := a.Store.SessionByToken(ownerToken); e != nil {
		t.Fatal("viewer sign out affected other principal", e)
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || cookies[0].MaxAge != -1 || !cookies[0].HttpOnly || !cookies[0].Secure {
		t.Fatal("logout cookie not removed securely", cookies)
	}
}

func v21Export(t *testing.T, a *App) protocol.Operation {
	t.Helper()
	now := a.Clock.Now()
	if e := a.Store.InsertAgent(&storage.AgentRow{ID: "v21-export", DesiredRevision: 1}, "fixture"); e != nil {
		t.Fatal(e)
	}
	value := 31.0
	if e := a.Store.InsertHostSample("v21-export", 1, "v21", now, &protocol.HostMetrics{CPUPercent: &value}); e != nil {
		t.Fatal(e)
	}
	w := httptest.NewRecorder()
	a.processSubmit(w, &storage.Session{Username: "owner", Role: "owner"}, protocol.SubmitOperation{Action: "history.export", ClientRequestKey: "v21-export", Params: map[string]any{"agent_id": "v21-export", "from": now.Add(-time.Second).Format(time.RFC3339Nano), "to": now.Add(time.Second).Format(time.RFC3339Nano)}})
	var op protocol.Operation
	if e := json.Unmarshal(w.Body.Bytes(), &op); e != nil || w.Code != 202 || op.Status != protocol.OpCompleted {
		t.Fatal("fixture export", e, w.Code, w.Body.String())
	}
	return op
}

func TestV21ExportRevalidatesDelayedBrowserAuthority(t *testing.T) {
	for _, change := range []string{"revoked", "expired", "demoted"} {
		t.Run(change, func(t *testing.T) {
			a, _ := testApp(t)
			clk := clock.NewFake(time.Now())
			a.Clock = clk
			a.Store.Clock = clk
			op := v21Export(t, a)
			token, s := v20Session(t, a, "owner")
			r := v21Request("GET", "/api/v1/exports/"+op.ID, token, s.CSRF)
			r.SetPathValue("id", op.ID)
			// Reproduce a request that passed middleware but waited while authority changed.
			handler := a.needAuth(func(w http.ResponseWriter, r *http.Request, snapshot *storage.Session) {
				var e error
				switch change {
				case "revoked":
					e = a.Store.DeleteSession(s.ID)
				case "expired":
					clk.Advance(time.Hour)
				case "demoted":
					_, e = a.Store.DB.Exec(`UPDATE admin_users SET role='viewer' WHERE id=?`, s.UserID)
				}
				if e != nil {
					t.Fatal(e)
				}
				a.handleExportDownload(w, r, snapshot)
			})
			w := httptest.NewRecorder()
			handler(w, r)
			if w.Code != 401 && w.Code != 403 {
				t.Fatalf("%s authority received export: %d %s", change, w.Code, w.Body.String())
			}
			if strings.Contains(w.Body.String(), `"samples"`) || w.Header().Get("Content-Disposition") != "" {
				t.Fatal("rejected request leaked export")
			}
		})
	}
}

func TestV21ExportStorageFailureIsNotMissingFile(t *testing.T) {
	a, _ := testApp(t)
	_, s := v20Session(t, a, "owner")
	if e := a.Store.Close(); e != nil {
		t.Fatal(e)
	}
	r := httptest.NewRequest("GET", "https://localhost/api/v1/exports/anything", nil)
	r.SetPathValue("id", "anything")
	w := httptest.NewRecorder()
	a.handleExportDownload(w, r, s)
	if w.Code != 503 {
		t.Fatalf("unavailable store misreported as missing export: %d %s", w.Code, w.Body.String())
	}
}

func TestV21ViewerLogoutDoesNotGrantOtherMutations(t *testing.T) {
	a, h := testApp(t)
	token, s := v20Session(t, a, "viewer")
	for _, path := range []string{"/api/v1/settings", "/api/v1/operations", "/api/v1/display/tv", "/api/v1/reauth", "/api/v1/account/password"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, v21Request("POST", path, token, s.CSRF))
		if w.Code != http.StatusForbidden {
			t.Fatalf("viewer mutation %s accepted: %d %s", path, w.Code, w.Body.String())
		}
	}
	for _, which := range []string{"csrf", "origin", "method"} {
		r := v21Request("POST", "/api/v1/logout", token, s.CSRF)
		switch which {
		case "csrf":
			r.Header.Del("X-CSRF-Token")
		case "origin":
			r.Header.Set("Origin", "https://other.invalid")
		case "method":
			r.Method = "GET"
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code == 200 {
			t.Fatalf("invalid logout accepted: %s", which)
		}
		if _, err := a.Store.SessionByToken(token); err != nil {
			t.Fatalf("invalid logout revoked session: %s %v", which, err)
		}
	}
}

func TestV21LogoutWriteFailurePreservesSessionAndDoesNotClaimSuccess(t *testing.T) {
	a, h := testApp(t)
	token, s := v20Session(t, a, "viewer")
	// Verify exact table name before installing an isolated temporary failure.
	if _, err := a.Store.DB.Exec(`CREATE TEMP TRIGGER v21_logout_fail BEFORE DELETE ON admin_sessions BEGIN SELECT RAISE(ABORT,'v21 fixture'); END`); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, v21Request("POST", "/api/v1/logout", token, s.CSRF))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("revocation failure misreported: %d %s", w.Code, w.Body.String())
	}
	if len(w.Result().Cookies()) != 0 {
		t.Fatal("failure hid local cookie while server token was not revoked")
	}
	if _, err := a.Store.SessionByToken(token); err != nil {
		t.Fatal(err)
	}
}
