package server

import (
	"encoding/json"
	"golang.org/x/net/websocket"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func v17Info(t *testing.T, call func(string, string, any) *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	w := call("GET", "/api/v1/agents/h/console", nil)
	var out map[string]any
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil {
		t.Fatal(w.Code, w.Body.String())
	}
	return out
}
func v17Target() consoleTarget {
	return consoleTarget{Host: "127.0.0.1", Port: 22, Username: "operator", Fingerprint: "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}
}
func v17Save(rev any, target consoleTarget) map[string]any {
	return map[string]any{"base_revision": rev, "enabled": true, "target": target, "fingerprint_verified": true}
}
func TestV17ConsoleConfigureEnableDisableAndTicketVersion(t *testing.T) {
	a, h := testApp(t)
	audit7Inventory(t, a)
	call := audit6HTTP(t, a, h, time.Minute)
	info := v17Info(t, call)
	if info["enabled"] != false || info["configurable"] != true {
		t.Fatal(info)
	}
	save := v17Save(info["config_revision"], v17Target())
	w := call("POST", "/api/v1/agents/h/console-config", save)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	info = v17Info(t, call)
	if info["enabled"] != true {
		t.Fatal(info)
	}
	file := filepath.Join(a.Cfg.DataDir, "console-targets.json")
	st, e := os.Stat(file)
	if e != nil || st.Mode().Perm() != 0600 {
		t.Fatal(st, e)
	}
	w = call("POST", "/api/v1/agents/h/console-config", save)
	if w.Code != 409 {
		t.Fatal("stale edit accepted", w.Code)
	}
	w = call("POST", "/api/v1/agents/h/console-ticket", map[string]any{"target_revision": info["target_revision"]})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	a.console.mu.Lock()
	n := len(a.console.tickets)
	a.console.mu.Unlock()
	if n != 1 {
		t.Fatal(n)
	}
	w = call("POST", "/api/v1/agents/h/console-config", map[string]any{"base_revision": info["config_revision"], "enabled": false})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if v17Info(t, call)["enabled"] != false {
		t.Fatal("not disabled")
	}
	a.console.mu.Lock()
	n = len(a.console.tickets)
	a.console.mu.Unlock()
	if n != 0 {
		t.Fatal("pending ticket survived disable")
	}
}
func TestV17ConsoleConfigureRequiresTrustAndRecentOwner(t *testing.T) {
	a, h := testApp(t)
	audit7Inventory(t, a)
	call := audit6HTTP(t, a, h, time.Minute)
	info := v17Info(t, call)
	body := v17Save(info["config_revision"], v17Target())
	body["fingerprint_verified"] = false
	if w := call("POST", "/api/v1/agents/h/console-config", body); w.Code != 400 {
		t.Fatal(w.Code)
	}
	body["fingerprint_verified"] = true
	old := audit6HTTP(t, a, h, -time.Minute)
	if w := old("POST", "/api/v1/agents/h/console-config", body); w.Code != 401 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, host := range []string{"169.254.169.254", "100.100.100.200", "example.org", "0.0.0.0", "fe80::1"} {
		target := v17Target()
		target.Host = host
		body["target"] = target
		if w := call("POST", "/api/v1/agents/h/console-config", body); w.Code != 400 {
			t.Fatal(host, w.Code)
		}
	}
	body["target"] = v17Target()
	body["password"] = "not-a-config-field"
	if w := call("POST", "/api/v1/agents/h/console-config", body); w.Code != 400 {
		t.Fatal("secret field accepted", w.Code)
	}
	if _, e := os.Stat(filepath.Join(a.Cfg.DataDir, "console-targets.json")); !os.IsNotExist(e) {
		t.Fatal("invalid edits touched config", e)
	}
}
func TestV17ConsoleConfigurePreservesOtherMachinesAndNoOp(t *testing.T) {
	a, h := testApp(t)
	audit7Inventory(t, a)
	call := audit6HTTP(t, a, h, time.Minute)
	b, _ := json.Marshal(map[string]any{"targets": map[string]consoleTarget{"other-machine": v17Target()}})
	path := filepath.Join(a.Cfg.DataDir, "console-targets.json")
	os.WriteFile(path, b, 0600)
	info := v17Info(t, call)
	if w := call("POST", "/api/v1/agents/h/console-config", v17Save(info["config_revision"], v17Target())); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	cfg, revision, e := a.readConsoleConfiguration()
	if e != nil || len(cfg.Targets) != 2 {
		t.Fatal(cfg, e)
	}
	before, _ := os.ReadFile(path)
	if w := call("POST", "/api/v1/agents/h/console-config", v17Save(revision, v17Target())); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("no-op rewrote file")
	}
}
func TestV17ConsoleAuditFailureBeforeAndAfterPublication(t *testing.T) {
	for _, phase := range []string{"requested", "saved"} {
		t.Run(phase, func(t *testing.T) {
			a, h := testApp(t)
			audit7Inventory(t, a)
			call := audit6HTTP(t, a, h, time.Minute)
			info := v17Info(t, call)
			_, e := a.Store.DB.Exec(`CREATE TRIGGER v17fail BEFORE INSERT ON audit_events WHEN NEW.action='console.configure.` + phase + `' BEGIN SELECT RAISE(FAIL,'audit failure'); END`)
			if e != nil {
				t.Fatal(e)
			}
			w := call("POST", "/api/v1/agents/h/console-config", v17Save(info["config_revision"], v17Target()))
			if w.Code != 503 {
				t.Fatal(w.Code, w.Body.String())
			}
			after := v17Info(t, call)
			if after["enabled"] != (phase == "saved") {
				t.Fatal("wrong commit boundary", after)
			}
			if phase == "saved" && !strings.Contains(w.Body.String(), "outcome_unknown") {
				t.Fatal("unconfirmed publication claimed failed/no effect")
			}
		})
	}
}
func TestV17ConsoleBadFileIsNotSilentlyOverwritten(t *testing.T) {
	a, h := testApp(t)
	audit7Inventory(t, a)
	call := audit6HTTP(t, a, h, time.Minute)
	info := v17Info(t, call)
	path := filepath.Join(a.Cfg.DataDir, "console-targets.json")
	os.WriteFile(path, []byte(`{"targets":null}`), 0600)
	w := call("POST", "/api/v1/agents/h/console-config", v17Save(info["config_revision"], v17Target()))
	if w.Code != 409 {
		t.Fatal(w.Code)
	}
	b, _ := os.ReadFile(path)
	if string(b) != `{"targets":null}` {
		t.Fatal("bad file overwritten")
	}
	if v17Info(t, call)["configurable"] != false {
		t.Fatal("bad trust file editable")
	}
}
func TestV17ConsoleAPISavedTargetConnectsOverSSH(t *testing.T) {
	target, _ := consoleSSHFixture(t)
	a, h := testApp(t)
	audit7Inventory(t, a)
	call := audit6HTTP(t, a, h, time.Minute)
	info := v17Info(t, call)
	if w := call("POST", "/api/v1/agents/h/console-config", v17Save(info["config_revision"], target)); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	ts := httptest.NewTLSServer(h)
	defer ts.Close()
	cookie, csrf := consoleLogin(t, ts)
	ws := openConsoleWS(t, ts, cookie, requestConsoleTicket(t, ts, cookie, csrf))
	defer ws.Close()
	ws.SetReadDeadline(time.Now().Add(8 * time.Second))
	var msg map[string]string
	for {
		if e := websocket.JSON.Receive(ws, &msg); e != nil {
			t.Fatal(e)
		}
		if msg["type"] == "ready" {
			break
		}
		if msg["type"] == "error" {
			t.Fatal(msg)
		}
	}
	// Config disable closes the actual existing connection, not just the UI flag.
	info = v17Info(t, call)
	w := call("POST", "/api/v1/agents/h/console-config", map[string]any{"base_revision": info["config_revision"], "enabled": false})
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	for {
		if e := websocket.JSON.Receive(ws, &msg); e != nil {
			break
		}
	}
}

func TestV17ConsoleHostKeySelectionDoesNotDowngradeRSA(t *testing.T) {
	for _, kind := range []string{"ssh-ed25519", "ecdsa-sha2-nistp256", "ecdsa-sha2-nistp384", "ecdsa-sha2-nistp521"} {
		v, ok := consoleHostKeyAlgorithms(kind)
		if !ok || len(v) != 1 || v[0] != kind {
			t.Fatal(kind, v, ok)
		}
	}
	v, ok := consoleHostKeyAlgorithms("ssh-rsa")
	if !ok || strings.Join(v, ",") != "rsa-sha2-512,rsa-sha2-256" {
		t.Fatal(v, ok)
	}
	for _, kind := range []string{"ssh-dss", "arbitrary", "ssh-ed25519-cert-v01@openssh.com"} {
		if _, ok := consoleHostKeyAlgorithms(kind); ok {
			t.Fatal(kind)
		}
	}
}
func TestV17ConcurrentSSHConfigEditsCannotOverwriteEachOther(t *testing.T) {
	a, h := testApp(t)
	audit7Inventory(t, a)
	call := audit6HTTP(t, a, h, time.Minute)
	info := v17Info(t, call)
	results := make(chan int, 2)
	start := make(chan struct{})
	for _, port := range []int{2222, 2223} {
		go func(port int) {
			<-start
			target := v17Target()
			target.Port = port
			results <- call("POST", "/api/v1/agents/h/console-config", v17Save(info["config_revision"], target)).Code
		}(port)
	}
	close(start)
	codes := []int{<-results, <-results}
	if !((codes[0] == 200 && codes[1] == 409) || (codes[0] == 409 && codes[1] == 200)) {
		t.Fatal(codes)
	}
}

func TestV17ConsoleRejectsCaseFoldedTrustFields(t *testing.T) {
	a, _ := testApp(t)
	audit7Inventory(t, a)
	target := v17Target()
	raw, _ := json.Marshal(target)
	for _, body := range []string{
		`{"targets":{"h":` + string(raw) + `},"Targets":{"h":` + string(raw) + `}}`,
		`{"targets":{"h":` + strings.TrimSuffix(string(raw), "}") + `,"Host":"192.0.2.10"}}}`,
	} {
		if e := os.WriteFile(filepath.Join(a.Cfg.DataDir, "console-targets.json"), []byte(body), 0600); e != nil {
			t.Fatal(e)
		}
		if _, e := a.consoleTarget("h"); e == nil {
			t.Fatal("case-folded field accepted")
		}
	}
}
