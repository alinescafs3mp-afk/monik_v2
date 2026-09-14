package setup

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"github.com/alinescafs3mp-afk/monik_v2/internal/agent/configfile"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAudit5AnnouncementTrustFailureNeverLeaksProof(t *testing.T) {
	hits := 0
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	defer ts.Close()
	other := httptest.NewTLSServer(http.NotFoundHandler())
	defer other.Close()
	// httptest TLS servers share a test cert; use invalid CA on an otherwise valid state.
	ca := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ts.Certificate().Raw}))
	p := &Profile{AutoDiscover: true, ControllerURL: ts.URL, StateDir: t.TempDir(), CACertPEM: ca}
	st, e := PrepareDiscovery(p)
	if e != nil {
		t.Fatal(e)
	}
	st.File.CACertPEM = "invalid"
	if _, e = AnnounceOnce(context.Background(), st); e == nil {
		t.Fatal("invalid trust accepted")
	}
	if hits != 0 {
		t.Fatal("sent proof without valid trust")
	}
	if !st.File.PendingRegistration {
		t.Fatal("changed registration")
	}
}
func TestAudit5AnnouncementRedirectAndInvalidConfirmation(t *testing.T) {
	hits := 0
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	defer target.Close()
	mode := "redirect"
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if mode == "redirect" {
			http.Redirect(w, r, target.URL, 307)
			return
		}
		var q protocol.Announcement
		_ = json.NewDecoder(r.Body).Decode(&q)
		json.NewEncoder(w).Encode(protocol.AnnouncementResponse{State: "approved", Fingerprint: protocol.RegistrationFingerprint(q.AgentID, q.Credential), Enrollment: &protocol.EnrollResponse{AgentID: "different", Credential: q.Credential, ControllerID: "controller", EndpointGeneration: 1}})
	}))
	defer ts.Close()
	p := &Profile{AutoDiscover: true, ControllerURL: ts.URL, StateDir: t.TempDir(), CACertPEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ts.Certificate().Raw}))}
	st, e := PrepareDiscovery(p)
	if e != nil {
		t.Fatal(e)
	}
	before, _ := os.ReadFile(filepath.Join(p.StateDir, "agent.json"))
	if _, e = AnnounceOnce(context.Background(), st); e == nil || hits != 0 {
		t.Fatal("redirect followed", e, hits)
	}
	mode = "invalid"
	if _, e = AnnounceOnce(context.Background(), st); e == nil {
		t.Fatal("wrong identity accepted")
	}
	after, _ := os.ReadFile(filepath.Join(p.StateDir, "agent.json"))
	if string(after) != string(before) {
		t.Fatal("invalid confirmation rewrote enrollment")
	}
	if _, e = PrepareDiscovery(p); e == nil {
		t.Fatal("setup overwrote existing identity")
	}
}
func TestAudit5PendingWaitStopsWithoutTelemetry(t *testing.T) {
	calls := 0
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/api/v1/agent/announce" {
			t.Error("unexpected preapproval request", r.URL.Path)
		}
		var q protocol.Announcement
		_ = json.NewDecoder(r.Body).Decode(&q)
		json.NewEncoder(w).Encode(protocol.AnnouncementResponse{State: "pending", Fingerprint: protocol.RegistrationFingerprint(q.AgentID, q.Credential)})
	}))
	defer ts.Close()
	p := &Profile{AutoDiscover: true, ControllerURL: ts.URL, StateDir: t.TempDir(), CACertPEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ts.Certificate().Raw}))}
	st, e := PrepareDiscovery(p)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if e = WaitForApproval(ctx, filepath.Join(p.StateDir, "agent.json"), io.Discard); e != context.DeadlineExceeded {
		t.Fatal(e)
	}
	loaded, e := configfile.Load(filepath.Join(p.StateDir, "agent.json"))
	if e != nil || !loaded.File.PendingRegistration || loaded.File.AgentID != st.File.AgentID {
		t.Fatal("pending identity lost", e)
	}
	if calls != 1 {
		t.Fatal(calls)
	}
}
func TestAudit5ConcurrentSetupLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	release, e := acquireSetupLock(path)
	if e != nil {
		t.Fatal(e)
	}
	if unlock, e := acquireSetupLock(path); e == nil {
		unlock()
		t.Fatal("concurrent setup allowed")
	}
	release()
	again, e := acquireSetupLock(path)
	if e != nil {
		t.Fatal(e)
	}
	again()
}

func TestAudit5DiscoveryAlternateConfigCannotOverwriteCredential(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("setup must not contact server") }))
	defer ts.Close()
	p := &Profile{AutoDiscover: true, ControllerURL: ts.URL, CACertPEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ts.Certificate().Raw})), StateDir: t.TempDir()}
	st, err := Enroll(p)
	if err != nil {
		t.Fatal(err)
	}
	before, err := configfile.ReadCredential(st.File.CredentialPath)
	if err != nil {
		t.Fatal(err)
	}
	p.ConfigPath = filepath.Join(p.StateDir, "another.json")
	if _, err = Enroll(p); err == nil {
		t.Fatal("alternate config bypassed identity protection")
	}
	after, err := configfile.ReadCredential(st.File.CredentialPath)
	if err != nil || before != after {
		t.Fatal("original proof overwritten", err)
	}
	p.AutoDiscover = false
	p.EnrollmentCode = "unused-fixture-code"
	if _, err = Enroll(p); err == nil {
		t.Fatal("code enrollment replaced automatic identity")
	}
}
