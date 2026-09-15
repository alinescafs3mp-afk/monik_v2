package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestV17ConsoleRejectsAmbiguousProtectedConfiguration(t *testing.T) {
	a, _ := testApp(t)
	audit7Inventory(t, a)
	target := consoleTarget{Host: "127.0.0.1", Port: 22, Username: "fixture", Fingerprint: "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}
	b, _ := json.Marshal(target)
	path := filepath.Join(a.Cfg.DataDir, "console-targets.json")
	cases := []string{
		`{"targets":{"h":` + string(b) + `},"targets":{"h":` + string(b) + `}}`,
		`{"targets":{"h":` + string(b) + `,"h":` + string(b) + `}}`,
	}
	for _, raw := range cases {
		if e := os.WriteFile(path, []byte(raw), 0600); e != nil {
			t.Fatal(e)
		}
		if _, e := a.consoleTarget("h"); e == nil {
			t.Fatal("ambiguous security configuration accepted")
		}
	}
}
func TestV17ConsoleRejectsConfigurationSymlink(t *testing.T) {
	a, _ := testApp(t)
	audit7Inventory(t, a)
	target := consoleTarget{Host: "127.0.0.1", Port: 22, Username: "fixture", Fingerprint: "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}
	b, _ := json.Marshal(map[string]any{"targets": map[string]consoleTarget{"h": target}})
	external := filepath.Join(t.TempDir(), "external.json")
	if e := os.WriteFile(external, b, 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(external, filepath.Join(a.Cfg.DataDir, "console-targets.json")); e != nil {
		t.Skip(e)
	}
	if _, e := a.consoleTarget("h"); e == nil {
		t.Fatal("symlink accepted as protected SSH trust file")
	}
}

func TestV17ConsoleTicketRejectsStaleDisplayedTarget(t *testing.T) {
	a, h := testApp(t)
	audit7Inventory(t, a)
	consoleConfig(t, a, consoleTarget{Host: "127.0.0.1", Port: 22, Username: "fixture", Fingerprint: "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"})
	call := audit6HTTP(t, a, h, time.Minute)
	w := call("POST", "/api/v1/agents/h/console-ticket", map[string]any{"target_revision": strings.Repeat("0", 64)})
	if w.Code != 409 {
		t.Fatalf("stale browser destination received a ticket: %d %s", w.Code, w.Body.String())
	}
}
