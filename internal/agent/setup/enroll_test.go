package setup

import (
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
)

func TestEnrollRetainsSelectedEndpointAndRetriesPersistedProof(t *testing.T) {
	var first protocol.EnrollRequest
	calls := 0
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req protocol.EnrollRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		calls++
		if calls == 1 {
			first = req
			w.Write([]byte(`{"agent_id":`))
			return
		}
		if req.AgentID != first.AgentID || req.Credential != first.Credential || len(req.Credential) != 64 {
			t.Error("retry lost the persisted identity")
		}
		json.NewEncoder(w).Encode(protocol.EnrollResponse{AgentID: req.AgentID, Credential: req.Credential, ControllerID: "controller", AdvertisedURL: "https://192.0.2.1:8777", EndpointGeneration: 1, ConfigRevision: 9})
	}))
	defer ts.Close()
	ca := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ts.Certificate().Raw}))
	p := &Profile{ControllerURL: ts.URL, CACertPEM: ca, EnrollmentCode: "test-code", StateDir: t.TempDir()}
	if _, err := Enroll(p); err == nil {
		t.Fatal("truncated enrollment response accepted")
	}
	if _, err := os.Stat(filepath.Join(p.StateDir, "enrollment-intent.json")); err != nil {
		t.Fatal("no retry intent")
	}
	got, err := Enroll(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.File.ControllerURL != ts.URL || got.File.CACertPEM != ca || got.File.AppliedRevision != 0 {
		t.Fatalf("bootstrap continuity lost: %+v", got.File)
	}
	if _, err = Enroll(p); err == nil {
		t.Fatal("setup overwrote enrolled identity")
	}
	if calls != 2 {
		t.Fatalf("unexpected requests: %d", calls)
	}
}
func TestEnrollDoesNotFollowRedirectsOrReplaceAnExistingConfiguration(t *testing.T) {
	hits := 0
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++; w.WriteHeader(500) }))
	defer target.Close()
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) }))
	defer ts.Close()
	p := &Profile{ControllerURL: ts.URL, CACertPEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ts.Certificate().Raw})), EnrollmentCode: "test", StateDir: t.TempDir()}
	if _, err := Enroll(p); err == nil {
		t.Fatal("redirect accepted")
	}
	if hits != 0 {
		t.Fatal("enrollment secrets followed a redirect")
	}
	path := filepath.Join(p.StateDir, "agent.json")
	if err := os.WriteFile(path, []byte("existing protected state"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Enroll(p); err == nil {
		t.Fatal("existing configuration replaced")
	}
	got, _ := os.ReadFile(path)
	if string(got) != "existing protected state" {
		t.Fatal("state changed")
	}
}
