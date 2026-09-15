package main

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"github.com/alinescafs3mp-afk/monik_v2/internal/installerbundle"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func fixtureSteps(log *[]string, c **installedConfig) steps {
	return steps{now: time.Now, preflight: func() error { *log = append(*log, "preflight"); return nil }, load: func() (*installedConfig, error) { return *c, nil }, active: func() bool { return true }, setup: func(context.Context, *installerbundle.Bundle) (*installedConfig, error) {
		*log = append(*log, "setup")
		*c = &installedConfig{AgentID: "agent", ControllerID: "controller", ControllerURL: "https://controller:8777"}
		return *c, nil
	}, install: func(_ context.Context, _ *installerbundle.Bundle, c *installedConfig) error {
		*log = append(*log, "install")
		c.Managed = true
		return nil
	}, ready: func(context.Context, *installedConfig) error { *log = append(*log, "ready"); return nil }}
}
func fixtureBundle() *installerbundle.Bundle {
	return &installerbundle.Bundle{Manifest: installerbundle.Manifest{Profile: &installerbundle.Profile{ControllerID: "controller", ExpiresAt: time.Now().Add(time.Hour)}}}
}
func TestV15OneRunCompletesOnlyAfterReady(t *testing.T) {
	var log []string
	var c *installedConfig
	s := fixtureSteps(&log, &c)
	var out bytes.Buffer
	if e := execute(context.Background(), fixtureBundle(), s, &out); e != nil {
		t.Fatal(e)
	}
	if strings.Join(log, ",") != "preflight,setup,install,ready" {
		t.Fatal(log)
	}
	if !strings.Contains(out.String(), "ГОТОВО.") {
		t.Fatal("no success after confirmation")
	}
}
func TestV15FailureAtEveryPhaseNeverPrintsReady(t *testing.T) {
	for _, phase := range []string{"preflight", "load", "setup", "install", "ready"} {
		t.Run(phase, func(t *testing.T) {
			var log []string
			var c *installedConfig
			s := fixtureSteps(&log, &c)
			e := fmt.Errorf("fixture failure")
			switch phase {
			case "preflight":
				s.preflight = func() error { return e }
			case "load":
				s.load = func() (*installedConfig, error) { return nil, e }
			case "setup":
				s.setup = func(context.Context, *installerbundle.Bundle) (*installedConfig, error) { return nil, e }
			case "install":
				s.install = func(context.Context, *installerbundle.Bundle, *installedConfig) error { return e }
			case "ready":
				s.ready = func(context.Context, *installedConfig) error { return e }
			}
			var out bytes.Buffer
			if execute(context.Background(), fixtureBundle(), s, &out) == nil || strings.Contains(out.String(), "ГОТОВО.") {
				t.Fatal("false ready")
			}
		})
	}
}
func TestV15RepeatRunningInstallDoesNotDowngradeOrReEnroll(t *testing.T) {
	var log []string
	c := &installedConfig{Managed: true, AgentID: "same", ControllerID: "controller", ControllerURL: "https://new-authorized-address"}
	s := fixtureSteps(&log, &c)
	b := fixtureBundle()
	b.Manifest.Profile.ExpiresAt = time.Now().Add(-time.Hour)
	var out bytes.Buffer
	if e := execute(context.Background(), b, s, &out); e != nil {
		t.Fatal(e)
	}
	if strings.Join(log, ",") != "preflight,ready" || !strings.Contains(out.String(), "https://new-authorized-address") {
		t.Fatal(log, out.String())
	}
}
func TestV15FreshExpiredFileDoesNotEnroll(t *testing.T) {
	var log []string
	var c *installedConfig
	s := fixtureSteps(&log, &c)
	b := fixtureBundle()
	b.Manifest.Profile.ExpiresAt = time.Now().Add(-time.Second)
	if e := execute(context.Background(), b, s, &bytes.Buffer{}); e == nil {
		t.Fatal("expired enrollment attempted")
	}
	if strings.Join(log, ",") != "preflight" {
		t.Fatal(log)
	}
}
func TestV15ForeignAndPendingIdentitiesArePreserved(t *testing.T) {
	for _, c := range []*installedConfig{{ControllerID: "other"}, {ControllerID: "controller", Pending: true}} {
		var log []string
		s := fixtureSteps(&log, &c)
		if execute(context.Background(), fixtureBundle(), s, &bytes.Buffer{}) == nil {
			t.Fatal("existing identity overwritten")
		}
		if len(log) != 1 {
			t.Fatal(log)
		}
	}
}
func TestV15InstallDoesNotClaimManagedWhenConfigDidNotCommit(t *testing.T) {
	var log []string
	var c *installedConfig
	s := fixtureSteps(&log, &c)
	s.install = func(context.Context, *installerbundle.Bundle, *installedConfig) error { return nil }
	if e := execute(context.Background(), fixtureBundle(), s, &bytes.Buffer{}); e == nil {
		t.Fatal("unmanaged accepted")
	}
}
func TestV15ControllerProofUsesPinnedTLSAndNoCredential(t *testing.T) {
	hits := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.Header.Get("Authorization") != "" {
			t.Error("credential sent during controller preflight")
		}
		json.NewEncoder(w).Encode(map[string]any{"product": "monik", "controller_id": "correct", "writable": true})
	}))
	defer server.Close()
	p := &installerbundle.Profile{ControllerURL: server.URL, CACertPEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})), ControllerID: "correct"}
	if e := verifyController(context.Background(), p); e != nil {
		t.Fatal(e)
	}
	p.ControllerID = "different"
	if e := verifyController(context.Background(), p); e == nil {
		t.Fatal("wrong controller accepted")
	}
	if hits != 2 {
		t.Fatal(hits)
	}
}
func TestV15StatusAuthHeadersAndDuplicateJSON(t *testing.T) {
	for _, body := range []string{`{"ready":true}`, `{"ready":false,"ready":true}`, `{"ready":true} {}`} {
		s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer credential" || r.Header.Get("X-Monik-Agent-Id") != "identity" {
				t.Error("wrong authentication format")
			}
			fmt.Fprint(w, body)
		}))
		client := clientFor(string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: s.Certificate().Raw})))
		var dst installationStatus
		e := readResponse(context.Background(), client, s.URL, "identity", "credential", &dst)
		client.CloseIdleConnections()
		s.Close()
		if (e == nil) != (body == `{"ready":true}`) {
			t.Fatal("ambiguous response accepted", e)
		}
	}
}
func TestV15SetupProfileUsesFixedProtectedPaths(t *testing.T) {
	b := encodeSetup(&installerbundle.Profile{ControllerURL: "https://controller", EnrollmentCode: "SECRET", CACertPEM: "CA"})
	var p map[string]any
	json.Unmarshal(b, &p)
	if p["state_dir"] != stateDir || p["config_path"] != configPath || p["enrollment_code"] != "SECRET" {
		t.Fatal(p)
	}
	if _, ok := p["auto_discover"]; ok {
		t.Fatal("pending admission substituted for prepared authorization")
	}
}
