package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
)

func testApp(t *testing.T) (*App, http.Handler) {
	t.Helper()
	app, err := Open(Config{DataDir: t.TempDir(), Listen: "127.0.0.1:0", AdvertisedURL: "https://192.168.12.128:8777"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Close() })
	if err := app.CompleteSetup(SetupRequest{Username: "owner", Password: "supersecret1", AdvertisedURL: "https://192.168.12.128:8777", Listen: "127.0.0.1:0"}); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	app.routes(mux)
	return app, app.csrfAndSecurity(mux)
}

func TestLoginOverviewEnrollReport(t *testing.T) {
	app, h := testApp(t)
	ts := httptest.NewServer(h)
	defer ts.Close()

	login, err := http.Post(ts.URL+"/api/v1/login", "application/json", bytes.NewReader([]byte(`{"username":"owner","password":"supersecret1"}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer login.Body.Close()
	if login.StatusCode != 200 {
		t.Fatalf("login %d", login.StatusCode)
	}
	var lr map[string]any
	_ = json.NewDecoder(login.Body).Decode(&lr)
	csrf, _ := lr["csrf"].(string)
	var cookie string
	for _, c := range login.Cookies() {
		if c.Name == "monik_session" {
			cookie = c.Value
		}
	}
	if cookie == "" || csrf == "" {
		t.Fatal("session")
	}

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/overview", nil)
	req.AddCookie(&http.Cookie{Name: "monik_session", Value: cookie})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("overview %d", resp.StatusCode)
	}

	body, _ := json.Marshal(map[string]any{
		"action": "enrollment.create", "client_request_key": "k-enroll", "params": map[string]any{},
	})
	req, _ = http.NewRequest(http.MethodPost, ts.URL+"/api/v1/operations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	req.AddCookie(&http.Cookie{Name: "monik_session", Value: cookie})
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var op protocol.Operation
	_ = json.NewDecoder(resp.Body).Decode(&op)
	resp.Body.Close()
	if resp.StatusCode >= 300 || len(op.Targets) == 0 {
		t.Fatalf("enroll op %d %+v", resp.StatusCode, op)
	}
	code, _ := op.Targets[0].Evidence["code"].(string)
	if code == "" {
		t.Fatal("no code")
	}

	enrollBody, _ := json.Marshal(protocol.EnrollRequest{Code: code, AgentID: "agent-1", Hostname: "testhost", OS: "linux", Arch: "amd64", Version: "0.1.0"})
	er, err := http.Post(ts.URL+"/api/v1/agent/enroll", "application/json", bytes.NewReader(enrollBody))
	if err != nil {
		t.Fatal(err)
	}
	var eresp protocol.EnrollResponse
	_ = json.NewDecoder(er.Body).Decode(&eresp)
	er.Body.Close()
	if er.StatusCode != 200 || eresp.Credential == "" {
		t.Fatalf("enroll %d %+v", er.StatusCode, eresp)
	}

	cpu := 12.0
	rep := protocol.AgentReport{
		SchemaVersion: 3, AgentID: "agent-1", SessionID: "s1", Sequence: 1, IsLive: true, ObservedAt: time.Now().UTC(),
		Host: &protocol.HostMetrics{Hostname: "testhost", OS: "linux", Arch: "amd64", CPUPercent: &cpu, RAMTotal: 100, RAMUsed: 10},
	}
	rb, _ := json.Marshal(rep)
	req, _ = http.NewRequest(http.MethodPost, ts.URL+"/api/v1/agent/report", bytes.NewReader(rb))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+eresp.Credential)
	req.Header.Set("X-Monik-Agent-Id", "agent-1")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("report %d", resp.StatusCode)
	}

	req, _ = http.NewRequest(http.MethodGet, ts.URL+"/api/v1/overview", nil)
	req.AddCookie(&http.Cookie{Name: "monik_session", Value: cookie})
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var ov map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&ov)
	resp.Body.Close()
	if ov["agents_total"].(float64) < 1 {
		t.Fatalf("%v", ov)
	}
	_ = app
}

func TestCSRFRejects(t *testing.T) {
	_, h := testApp(t)
	ts := httptest.NewServer(h)
	defer ts.Close()
	login, _ := http.Post(ts.URL+"/api/v1/login", "application/json", bytes.NewReader([]byte(`{"username":"owner","password":"supersecret1"}`)))
	login.Body.Close()
	var cookie string
	for _, c := range login.Cookies() {
		if c.Name == "monik_session" {
			cookie = c.Value
		}
	}
	body, _ := json.Marshal(map[string]any{"action": "preference.save", "client_request_key": "x", "params": map[string]any{}})
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/operations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "monik_session", Value: cookie})
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatalf("want 403, got %d", resp.StatusCode)
	}
}

func TestIdempotentSubmit(t *testing.T) {
	_, h := testApp(t)
	ts := httptest.NewServer(h)
	defer ts.Close()
	login, _ := http.Post(ts.URL+"/api/v1/login", "application/json", bytes.NewReader([]byte(`{"username":"owner","password":"supersecret1"}`)))
	var lr map[string]any
	_ = json.NewDecoder(login.Body).Decode(&lr)
	login.Body.Close()
	csrf, _ := lr["csrf"].(string)
	var cookie string
	for _, c := range login.Cookies() {
		if c.Name == "monik_session" {
			cookie = c.Value
		}
	}
	mk := func() *http.Response {
		body, _ := json.Marshal(map[string]any{"action": "preference.save", "client_request_key": "same", "params": map[string]any{"density": "comfortable"}})
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/operations", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-CSRF-Token", csrf)
		req.AddCookie(&http.Cookie{Name: "monik_session", Value: cookie})
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	r1 := mk()
	var op1 protocol.Operation
	_ = json.NewDecoder(r1.Body).Decode(&op1)
	r1.Body.Close()
	r2 := mk()
	var op2 protocol.Operation
	_ = json.NewDecoder(r2.Body).Decode(&op2)
	r2.Body.Close()
	if op1.ID == "" || op1.ID != op2.ID {
		t.Fatalf("%s vs %s", op1.ID, op2.ID)
	}
}
