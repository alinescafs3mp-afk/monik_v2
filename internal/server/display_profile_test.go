package server

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/storage"
)

func tvBody(rev int, id string) map[string]any {
	return map[string]any{"expected_revision": rev, "request_id": id, "value": storage.DefaultTVLayout()}
}

const tvRequestID = "11111111-2222-3333-4444-555555555555"

func TestV19TVHTTPAuthAndStrictPayloads(t *testing.T) {
	a, h := testApp(t)
	u, _ := a.Store.UserByName("owner")
	token, s, e := a.Store.CreateSession(u, time.Hour, time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	request := func(method, raw, role, csrf, origin string) *httptest.ResponseRecorder {
		t.Helper()
		a.Store.DB.Exec(`UPDATE admin_users SET role=? WHERE id=?`, role, u.ID)
		r := httptest.NewRequest(method, "https://localhost/api/v1/display/tv", strings.NewReader(raw))
		r.AddCookie(&http.Cookie{Name: "monik_session", Value: token})
		r.Header.Set("X-CSRF-Token", csrf)
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	raw, _ := json.Marshal(tvBody(0, tvRequestID))
	for _, tt := range []struct {
		role, csrf, origin string
		status             int
	}{{"owner", "", "", 403}, {"viewer", s.CSRF, "", 403}, {"operator", s.CSRF, "", 403}, {"owner", s.CSRF, "https://evil.invalid", 403}, {"owner", s.CSRF, "null", 403}} {
		w := request("POST", string(raw), tt.role, tt.csrf, tt.origin)
		if w.Code != tt.status {
			t.Fatal(tt, w.Code, w.Body.String())
		}
	}
	w := request("GET", "", "viewer", "", "")
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Code, w.Header())
	}
	bad := []string{`null`, `{}`, string(raw) + `{}`, strings.Replace(string(raw), `"value":`, `"VALUE":`, 1), strings.Replace(string(raw), `"autoplay":false`, `"autoplay":null`, 1), strings.Replace(string(raw), `"autoplay":false`, `"autoplay":false,"autoplay":true`, 1), strings.Replace(string(raw), `"expected_revision":0`, `"expected_revision":null`, 1), strings.Replace(string(raw), tvRequestID, strings.Repeat("z", 36), 1), string(raw) + strings.Repeat(" ", 4096)}
	for _, b := range bad {
		w := request("POST", b, "owner", s.CSRF, "")
		if w.Code != 400 {
			t.Fatalf("malformed accepted %d %s", w.Code, w.Body.String())
		}
	}
	w = request("POST", string(raw), "owner", s.CSRF, "https://localhost")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"saved":true`) {
		t.Fatal(w.Code, w.Body.String())
	}
	if e = a.Store.DeleteSession(s.ID); e != nil {
		t.Fatal(e)
	}
	w = request("GET", "", "owner", s.CSRF, "")
	if w.Code != 401 {
		t.Fatal("revoked session read profile", w.Code)
	}
}
func TestV19TVRealHTTPSAndSSEBetweenTwoSessions(t *testing.T) {
	a, h := testApp(t)
	ts := httptest.NewTLSServer(h)
	defer ts.Close()
	u, _ := a.Store.UserByName("owner")
	tokenA, sessionA, _ := a.Store.CreateSession(u, time.Hour, time.Minute)
	tokenB, _, _ := a.Store.CreateSession(u, time.Hour, time.Minute)
	client := ts.Client()
	client.Timeout = 8 * time.Second
	r, _ := http.NewRequest("GET", ts.URL+"/api/v1/events", nil)
	r.AddCookie(&http.Cookie{Name: "monik_session", Value: tokenB})
	stream, e := client.Do(r)
	if e != nil {
		t.Fatal(e)
	}
	defer stream.Body.Close()
	results := make(chan string, 1)
	go func() {
		scan := bufio.NewScanner(stream.Body)
		for scan.Scan() {
			if scan.Text() == "event: display" {
				results <- scan.Text()
				return
			}
		}
		results <- "closed"
	}()
	raw, _ := json.Marshal(tvBody(0, tvRequestID))
	r, _ = http.NewRequest("POST", ts.URL+"/api/v1/display/tv", bytes.NewReader(raw))
	r.AddCookie(&http.Cookie{Name: "monik_session", Value: tokenA})
	r.Header.Set("X-CSRF-Token", sessionA.CSRF)
	r.Header.Set("Origin", ts.URL)
	response, e := client.Do(r)
	if e != nil {
		t.Fatal(e)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal(response.StatusCode)
	}
	select {
	case msg := <-results:
		if msg != "event: display" {
			t.Fatal(msg)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("second session did not receive display event")
	}
	r, _ = http.NewRequest("GET", ts.URL+"/api/v1/display/tv", nil)
	r.AddCookie(&http.Cookie{Name: "monik_session", Value: tokenB})
	response, e = client.Do(r)
	if e != nil {
		t.Fatal(e)
	}
	defer response.Body.Close()
	var got struct {
		Profile      storage.TVProfile `json:"profile"`
		ControllerID string            `json:"controller_id"`
	}
	if e = json.NewDecoder(response.Body).Decode(&got); e != nil || got.Profile.Revision != 1 || got.ControllerID != a.ControllerID() {
		t.Fatal(got, e)
	}
	if got.Profile.LastRequestID != tvRequestID {
		t.Fatal(got)
	}
}
func TestV19TVNoSuccessOnFailedTransaction(t *testing.T) {
	a, h := testApp(t)
	call := audit6HTTP(t, a, h, time.Minute)
	_, e := a.Store.DB.Exec(`CREATE TRIGGER profile_failure BEFORE INSERT ON event_log WHEN NEW.type='display' BEGIN SELECT RAISE(ABORT,'display failure'); END`)
	if e != nil {
		t.Fatal(e)
	}
	w := call("POST", "/api/v1/display/tv", tvBody(0, tvRequestID))
	if w.Code != 503 {
		t.Fatal(w.Code, w.Body.String())
	}
	p, e := a.Store.TVProfile()
	if e != nil || p.Revision != 0 {
		t.Fatal(p, e)
	}
}
func TestV19TVRateBoundIsVisible(t *testing.T) {
	a, h := testApp(t)
	call := audit6HTTP(t, a, h, time.Minute)
	for i := 0; i < 60; i++ {
		w := call("POST", "/api/v1/display/tv", tvBody(i, fmt.Sprintf("11111111-2222-3333-4444-%012d", i)))
		if w.Code != 200 {
			t.Fatal(i, w.Code, w.Body.String())
		}
	}
	if w := call("POST", "/api/v1/display/tv", tvBody(60, tvRequestID)); w.Code != 429 {
		t.Fatal(w.Code)
	}
}
