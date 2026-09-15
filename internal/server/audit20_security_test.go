package server

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestV20OriginPolicy(t *testing.T) {
	cases := []struct {
		name, origin, referer, site, ct string
		status                          int
	}{
		{"native-json", "", "", "", "application/json", 200},
		{"same-origin", "https://localhost", "", "same-origin", "application/json; charset=utf-8", 200},
		{"default-port", "https://localhost:443", "", "", "application/json", 200},
		{"null", "null", "", "", "application/json", 403},
		{"foreign", "https://example.invalid", "", "", "application/json", 403},
		{"wrong-port", "https://localhost:444", "", "", "application/json", 403},
		{"http-downgrade", "http://localhost", "", "", "application/json", 403},
		{"userinfo", "https://user@localhost", "", "", "application/json", 403},
		{"origin-path", "https://localhost/path", "", "", "application/json", 403},
		{"origin-query", "https://localhost?anything", "", "", "application/json", 403},
		{"empty-fragment", "https://localhost#", "", "", "application/json", 403},
		{"referer", "", "https://localhost/login", "", "application/json", 200},
		{"foreign-referer", "", "https://example.invalid/", "", "application/json", 403},
		{"cross-site", "", "", "cross-site", "application/json", 403},
		{"sibling-site", "", "", "same-site", "application/json", 403},
		{"simple-body", "", "", "", "text/plain", 415},
		{"missing-type", "", "", "", "", 415},
	}
	a := &App{}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "https://localhost/api/v1/login", nil)
			for k, v := range map[string]string{"Origin": c.origin, "Referer": c.referer, "Sec-Fetch-Site": c.site, "Content-Type": c.ct} {
				if v != "" {
					r.Header.Set(k, v)
				}
			}
			w := httptest.NewRecorder()
			ok := a.browserRequestAllowed(w, r)
			if ok {
				w.WriteHeader(200)
			}
			if w.Code != c.status {
				t.Fatalf("got HTTP %d want %d", w.Code, c.status)
			}
		})
	}
	r := httptest.NewRequest("POST", "https://localhost/api/v1/login", nil)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Add("Origin", "https://localhost")
	r.Header.Add("Origin", "https://localhost")
	if a.browserRequestAllowed(httptest.NewRecorder(), r) {
		t.Fatal("duplicate Origin accepted")
	}
}

func TestV20PasswordCapacityIsSharedAndReleased(t *testing.T) {
	a, h := testApp(t)
	var releases []func()
	for i := 0; i < maxPasswordWork; i++ {
		release, ok := a.passwordWork(httptest.NewRecorder())
		if !ok {
			t.Fatal("capacity lost")
		}
		releases = append(releases, release)
		defer release()
	}
	r := httptest.NewRequest("POST", "https://localhost/api/v1/login", strings.NewReader(`{"username":"owner","password":"supersecret1"}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 503 || w.Header().Get("Retry-After") == "" {
		t.Fatal("saturated login admitted KDF", w.Code)
	}
	call := audit6HTTP(t, a, h, time.Minute)
	for _, v := range []struct {
		path string
		body any
	}{
		{"/api/v1/reauth", map[string]string{"password": "supersecret1"}},
		{"/api/v1/account/password", map[string]string{"current_password": "supersecret1", "new_password": "new-testing-password"}},
	} {
		w := call("POST", v.path, v.body)
		if w.Code != 503 {
			t.Fatal("password path bypassed shared capacity", v.path, w.Code)
		}
	}
	releases[0]()
	releases[0]() // duplicate cleanup must not underflow.
	release, ok := a.passwordWork(httptest.NewRecorder())
	if !ok {
		t.Fatal("permit not released")
	}
	defer release()
	if _, ok = a.passwordWork(httptest.NewRecorder()); ok {
		t.Fatal("duplicate cleanup increased limit")
	}
}

func TestV20EventStreamLimits(t *testing.T) {
	a := &App{}
	var closeAll []func()
	defer func() {
		for _, f := range closeAll {
			f()
		}
	}()
	for i := 0; i < maxEventStreams; i++ {
		key := fmt.Sprintf("s-%d", i/maxSessionEventStreams)
		f, ok := a.eventStreamSlot(key)
		if !ok {
			t.Fatalf("early saturation %d", i)
		}
		closeAll = append(closeAll, f)
	}
	if _, ok := a.eventStreamSlot("other"); ok {
		t.Fatal("global stream cap bypassed")
	}
	closeAll[0]()
	closeAll[0]()
	if _, ok := a.eventStreamSlot("s-1"); ok {
		t.Fatal("per-session cap bypassed")
	}
	f, ok := a.eventStreamSlot("s-0")
	if !ok {
		t.Fatal("release not usable")
	}
	closeAll = append(closeAll, f)
	if _, ok := a.eventStreamSlot(""); ok {
		t.Fatal("anonymous slot accepted")
	}
}

type v20BrokenWriter struct {
	*httptest.ResponseRecorder
	writes int
}

func (w *v20BrokenWriter) Write(b []byte) (int, error) { w.writes++; return 0, io.ErrClosedPipe }
func TestV20SSEInitialFailureReleasesSlot(t *testing.T) {
	a, _ := testApp(t)
	_, s := v20Session(t, a, "owner")
	w := &v20BrokenWriter{ResponseRecorder: httptest.NewRecorder()}
	a.handleSSE(w, httptest.NewRequest("GET", "https://localhost/api/v1/events", nil), s)
	if w.writes != 1 {
		t.Fatal("initial write not checked")
	}
	a.network.mu.Lock()
	defer a.network.mu.Unlock()
	if a.network.streams != 0 || len(a.network.sessions) != 0 {
		t.Fatal("failed stream leaked capacity")
	}
}

func TestV20SSERealHTTPSLimitAndRevocation(t *testing.T) {
	a, h := testApp(t)
	token, s := v20Session(t, a, "owner")
	ts := httptest.NewTLSServer(h)
	defer ts.Close()
	var bodies []io.ReadCloser
	defer func() {
		for _, b := range bodies {
			b.Close()
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	open := func() *http.Response {
		t.Helper()
		r, _ := http.NewRequestWithContext(ctx, "GET", ts.URL+"/api/v1/events", nil)
		r.AddCookie(&http.Cookie{Name: "monik_session", Value: token})
		res, e := ts.Client().Do(r)
		if e != nil {
			t.Fatal(e)
		}
		return res
	}
	var reader *bufio.Reader
	for i := 0; i < maxSessionEventStreams; i++ {
		res := open()
		if res.StatusCode != 200 {
			t.Fatal(res.StatusCode)
		}
		bodies = append(bodies, res.Body)
		br := bufio.NewReader(res.Body)
		for {
			line, e := br.ReadString('\n')
			if e != nil {
				t.Fatal(e)
			}
			if line == "\n" {
				break
			}
		}
		if i == 0 {
			reader = br
		}
	}
	limited := open()
	limited.Body.Close()
	if limited.StatusCode != 429 {
		t.Fatal("HTTP stream cap absent", limited.StatusCode)
	}
	if e := a.Store.DeleteSession(s.ID); e != nil {
		t.Fatal(e)
	}
	_, e := io.ReadAll(reader)
	if !errors.Is(e, nil) {
		t.Fatal("stream did not terminate cleanly on revoke", e)
	}
	a.network.mu.Lock()
	count := a.network.streams
	a.network.mu.Unlock()
	// Independent streams see revocation on their own one-second checks.
	deadline := time.Now().Add(3 * time.Second)
	for count != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		a.network.mu.Lock()
		count = a.network.streams
		a.network.mu.Unlock()
	}
	if count != 0 {
		t.Fatal("revoked streams retained slots", count)
	}
}

func TestV20BrowserAndAgentAuthorityStaySeparate(t *testing.T) {
	a, h := testApp(t)
	audit7Inventory(t, a)
	token, s := v20Session(t, a, "viewer")
	for _, path := range []string{"/api/v1/overview", "/api/v1/operations", "/api/v1/secrets", "/api/v1/agents/h/agent-console", "/api/v1/agents/h/console"} {
		r := httptest.NewRequest("GET", "https://localhost"+path, nil)
		r.Header.Set("Authorization", "Bearer fixture")
		r.Header.Set("X-Monik-Agent-Id", "h")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatal("agent key granted browser access", path, w.Code)
		}
	}
	for _, path := range []string{"/api/v1/operations", "/api/v1/display/tv", "/api/v1/agents/h/console-config", "/api/v1/agents/h/agent-console-ticket", "/api/v1/installers/linux-amd64"} {
		r := httptest.NewRequest("POST", "https://localhost"+path, strings.NewReader(`{}`))
		r.AddCookie(&http.Cookie{Name: "monik_session", Value: token})
		r.Header.Set("X-CSRF-Token", s.CSRF)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatal("viewer mutated", path, w.Code)
		}
	}
	owner, session := v20Session(t, a, "owner")
	r := httptest.NewRequest("POST", "https://localhost/api/v1/operations", strings.NewReader(`{}`))
	r.AddCookie(&http.Cookie{Name: "monik_session", Value: owner})
	r.Header.Set("X-CSRF-Token", session.CSRF)
	r.Header.Set("Origin", "https://attacker.invalid")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("cookie/CSRF skipped Origin", w.Code)
	}
	r = httptest.NewRequest("GET", "https://localhost/api/v1/overview", nil)
	r.AddCookie(&http.Cookie{Name: "monik_session", Value: owner})
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Header().Get("Content-Security-Policy"), "base-uri 'none'") {
		t.Fatal("missing private response protections")
	}
}

func TestV20NoForwardedHeaderAuthentication(t *testing.T) {
	a, h := testApp(t)
	a.Cfg.AdminAllowlist = []string{"127.0.0.0/8"}
	r := httptest.NewRequest("POST", "https://localhost/api/v1/login", strings.NewReader(`{"username":"owner","password":"supersecret1"}`))
	r.RemoteAddr = "198.51.100.7:12345"
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Forwarded-For", "127.0.0.1")
	r.Header.Set("Forwarded", "for=127.0.0.1")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("forwarded header bypassed network policy", w.Code)
	}
}

func TestV20DuplicateTransportIdentityRejected(t *testing.T) {
	r := httptest.NewRequest("GET", "https://localhost/", nil)
	r.Header.Set("Authorization", "Bearer fixture")
	r.Header.Set("X-Monik-Agent-Id", "h")
	if _, _, ok := bearer(r); !ok {
		t.Fatal("single credential rejected")
	}
	r.Header.Add("Authorization", "Bearer another")
	if _, _, ok := bearer(r); ok {
		t.Fatal("duplicate credentials accepted")
	}
	r.Header.Set("Authorization", "Bearer fixture")
	r.Header.Add("X-Monik-Agent-Id", "other")
	if _, _, ok := bearer(r); ok {
		t.Fatal("duplicate machine identity accepted")
	}
	r.Header.Add("Origin", "https://localhost")
	r.Header.Add("Origin", "https://localhost")
	if consoleOrigin(r) {
		t.Fatal("duplicate websocket origin accepted")
	}
}

func FuzzV20BrowserOrigin(f *testing.F) {
	for _, s := range []string{"https://localhost", "null", "https://localhost:443", "https://localhost@attacker.invalid", "https://[::1]", "https://localhost/", "https://localhost#"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, origin string) {
		if len(origin) > 4096 {
			return
		}
		r := httptest.NewRequest("POST", "https://localhost/login", nil)
		if sameBrowserOrigin(r, origin, true) && !(origin == "https://localhost" || origin == "https://localhost:443" || strings.EqualFold(origin, "https://localhost") || strings.EqualFold(origin, "https://localhost:443")) {
			t.Fatalf("unexpected accepted origin %q", origin)
		}
	})
}
