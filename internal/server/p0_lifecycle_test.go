package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"github.com/alinescafs3mp-afk/monik_v2/internal/secure"
	"github.com/alinescafs3mp-afk/monik_v2/internal/storage"
	"github.com/alinescafs3mp-afk/monik_v2/internal/tufutil"
)

func ownerRecent(t *testing.T) *storage.Session {
	t.Helper()
	until := time.Now().Add(time.Hour)
	return &storage.Session{Username: "owner", Role: "owner", RecentAuthUntil: &until}
}

func TestSecretReplaceStripsPlaintextAndStoresSealedBlob(t *testing.T) {
	app, _ := testApp(t)
	if err := app.Store.InsertAgent(&storage.AgentRow{ID: "a", Hostname: "h", OS: "linux", Arch: "amd64", DesiredConfig: "{}"}, "hash"); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	_ = app.Store.TouchAgent("a", "s1", 1, true, &protocol.HostMetrics{Hostname: "h", OS: "linux", Arch: "amd64"}, nil, map[string]string{})
	_, _ = app.Store.DB.Exec(`UPDATE agents SET last_live_at=? WHERE id='a'`, now.Format(time.RFC3339Nano))
	const secret = "DO_NOT_PERSIST_TEST_SECRET"
	w := httptest.NewRecorder()
	app.processSubmit(w, ownerRecent(t), protocol.SubmitOperation{
		Action: "secret.replace", ClientRequestKey: "sec-1", TargetIDs: []string{"a"},
		Params: map[string]any{"name": "token", "header": "X-Token", "value": secret},
	})
	if w.Code >= 500 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	raw := w.Body.String()
	if strings.Contains(raw, secret) {
		t.Fatal("plaintext leaked in response")
	}
	var blob string
	if err := app.Store.DB.QueryRow(`SELECT params FROM operations`).Scan(&blob); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(blob, secret) {
		t.Fatal("plaintext in operation params")
	}
	if err := app.Store.DB.QueryRow(`SELECT envelope FROM agent_jobs`).Scan(&blob); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(blob, secret) {
		t.Fatal("plaintext in job envelope")
	}
	var n int
	if err := app.Store.DB.QueryRow(`SELECT COUNT(*) FROM check_secrets`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("secrets=%d %v", n, err)
	}
	id := ""
	if err := app.Store.DB.QueryRow(`SELECT id FROM check_secrets`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	_, _, _, _, nonce, ct, err := app.Store.SecretForAgent(id, "a")
	if err != nil {
		t.Fatal(err)
	}
	plain, err := secure.Open(app.Master, nonce, ct)
	if err != nil || string(plain) != secret {
		t.Fatalf("roundtrip %q %v", plain, err)
	}
}

func TestRebindPrepareDoesNotRewriteAdvertisedURLAndActivateRequiresPlan(t *testing.T) {
	app, _ := testApp(t)
	before := app.Cfg.AdvertisedURL
	if err := app.Store.InsertAgent(&storage.AgentRow{ID: "a", Hostname: "h", OS: "linux", Arch: "amd64", DesiredConfig: "{}"}, "hash"); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	_, _ = app.Store.DB.Exec(`UPDATE agents SET last_live_at=? WHERE id='a'`, now.Format(time.RFC3339Nano))
	w := httptest.NewRecorder()
	app.processSubmit(w, ownerRecent(t), protocol.SubmitOperation{
		Action: "rebind.prepare", ClientRequestKey: "rb-prep", TargetIDs: []string{"a"},
		Params: map[string]any{"candidate_url": "https://192.0.2.8:8777"},
	})
	if w.Code >= 400 {
		t.Fatalf("prepare: %d %s", w.Code, w.Body.String())
	}
	if app.Cfg.AdvertisedURL != before {
		t.Fatalf("prepare rewrote advertised url: %s", app.Cfg.AdvertisedURL)
	}
	w = httptest.NewRecorder()
	app.processSubmit(w, ownerRecent(t), protocol.SubmitOperation{
		Action: "rebind.activate", ClientRequestKey: "rb-act-none", TargetIDs: []string{"nobody"},
		Params: map[string]any{"plan_id": "missing"},
	})
	if w.Code == 501 {
		t.Fatal("activate still fail-closed")
	}
	w = httptest.NewRecorder()
	app.processSubmit(w, ownerRecent(t), protocol.SubmitOperation{
		Action: "rebind.activate", ClientRequestKey: "rb-act-unprepared", TargetIDs: []string{"a"},
		Params: map[string]any{"plan_id": "not-this-plan"},
	})
	var op protocol.Operation
	if err := json.Unmarshal(w.Body.Bytes(), &op); err != nil {
		t.Fatal(err, w.Body.String())
	}
	found := false
	for _, tget := range op.Targets {
		if tget.AgentID == "a" && (tget.Status == protocol.TargetRejected || tget.Status == protocol.TargetFailed) {
			found = true
		}
		if tget.Status == protocol.TargetSucceeded || tget.Status == protocol.TargetQueued || tget.Status == protocol.TargetAccepted {
			t.Fatalf("unprepared activate must not proceed: %s", w.Body.String())
		}
	}
	if !found {
		t.Fatalf("unprepared activate must reject: %s", w.Body.String())
	}
}

func TestImportTrustedAndUnmanagedRollout(t *testing.T) {
	app, _ := testApp(t)
	keys := t.TempDir()
	repo := t.TempDir()
	ks, err := tufutil.InitKeys(keys)
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "agent")
	if err := os.WriteFile(bin, []byte("worker-bytes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := tufutil.SignRepository(ks, repo, map[string]string{"linux-amd64/monik-agent": bin}, 30, 2); err != nil {
		t.Fatal(err)
	}
	bundle := filepath.Join(t.TempDir(), "rel.tgz")
	if err := tufutil.PackBundle(repo, bundle, "0.2.0", "p0"); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	app.processSubmit(w, ownerRecent(t), protocol.SubmitOperation{
		Action: "update.import", ClientRequestKey: "imp-1",
		Params: map[string]any{"bundle_path": bundle, "enroll_root": true},
	})
	if w.Code >= 400 {
		t.Fatalf("import: %d %s", w.Code, w.Body.String())
	}
	if _, err := app.updateRoot(); err != nil {
		t.Fatal(err)
	}
	rels, err := app.Store.Releases()
	if err != nil || len(rels) != 1 {
		t.Fatalf("releases %+v %v", rels, err)
	}
	releaseID, _ := rels[0]["id"].(string)
	if err := app.Store.InsertAgent(&storage.AgentRow{ID: "a", Hostname: "h", OS: "linux", Arch: "amd64", DesiredConfig: "{}"}, "hash"); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	_, _ = app.Store.DB.Exec(`UPDATE agents SET last_live_at=? WHERE id='a'`, now.Format(time.RFC3339Nano))
	w = httptest.NewRecorder()
	app.processSubmit(w, ownerRecent(t), protocol.SubmitOperation{
		Action: "update.rollout", ClientRequestKey: "roll-1", TargetIDs: []string{"a"},
		Params: map[string]any{"release_id": releaseID},
	})
	var op protocol.Operation
	if err := json.Unmarshal(w.Body.Bytes(), &op); err != nil {
		t.Fatal(w.Body.String())
	}
	if len(op.Targets) == 0 || op.Targets[0].Status != protocol.TargetUnsupported {
		t.Fatalf("unmanaged rollout must be unsupported: %+v", op.Targets)
	}
}

func TestLifecycleConflictRejectsSecondDisruptiveJob(t *testing.T) {
	app, _ := testApp(t)
	if err := app.Store.InsertAgent(&storage.AgentRow{ID: "a", Hostname: "h", OS: "linux", Arch: "amd64", ManagedReady: true, DesiredConfig: "{}"}, "hash"); err != nil {
		t.Fatal(err)
	}
	_, _ = app.Store.DB.Exec(`UPDATE agents SET last_live_at=?, managed_ready=1 WHERE id='a'`, time.Now().UTC().Format(time.RFC3339Nano))
	w := httptest.NewRecorder()
	app.processSubmit(w, ownerRecent(t), protocol.SubmitOperation{
		Action: "agent.restart", ClientRequestKey: "rst-1", TargetIDs: []string{"a"}, Params: map[string]any{},
	})
	if w.Code >= 400 && w.Code != 202 {
		t.Fatalf("restart: %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	app.processSubmit(w, ownerRecent(t), protocol.SubmitOperation{
		Action: "update.rollout", ClientRequestKey: "roll-conflict", TargetIDs: []string{"a"},
		Params: map[string]any{"release_id": "none"},
	})
	var op protocol.Operation
	_ = json.Unmarshal(w.Body.Bytes(), &op)
	if len(op.Targets) == 0 || op.Targets[0].Status != protocol.TargetRejected {
		t.Fatalf("expected lifecycle conflict, got %+v %s", op.Targets, w.Body.String())
	}
}

func TestCheckTrialDoesNotDialFromController(t *testing.T) {
	app, _ := testApp(t)
	hits := 0
	ts := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits++ }))
	t.Cleanup(ts.Close)
	if err := app.Store.InsertAgent(&storage.AgentRow{ID: "a", Hostname: "h", DesiredConfig: "{}"}, "hash"); err != nil {
		t.Fatal(err)
	}
	_, _ = app.Store.DB.Exec(`UPDATE agents SET last_live_at=? WHERE id='a'`, time.Now().UTC().Format(time.RFC3339Nano))
	w := httptest.NewRecorder()
	app.processSubmit(w, ownerRecent(t), protocol.SubmitOperation{
		Action: "check.trial", ClientRequestKey: "trial-1", TargetIDs: []string{"a"},
		Params: map[string]any{"url": ts.URL, "method": "GET"},
	})
	if w.Code >= 400 {
		t.Fatalf("trial: %d %s", w.Code, w.Body.String())
	}
	if hits != 0 {
		t.Fatalf("controller dialed the trial URL %d times", hits)
	}
	raw := w.Body.String()
	if strings.Contains(raw, "DO_NOT") {
		t.Fatal(raw)
	}
}

func TestPendingCredentialIsAcceptedThenOldIsRetired(t *testing.T) {
	app, h := testApp(t)
	old := "old-token-value"
	if err := app.Store.InsertAgent(&storage.AgentRow{ID: "a", Hostname: "h", DesiredConfig: "{}"}, secure.HashToken(old)); err != nil {
		t.Fatal(err)
	}
	next := "next-token-value"
	until := time.Now().Add(time.Minute)
	if err := app.Store.SetPendingCredential("a", secure.HashToken(next), "job", until); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/api/v1/agent/update-root", nil)
	req.Header.Set("Authorization", "Bearer "+next)
	req.Header.Set("X-Monik-Agent-Id", "a")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code == 401 {
		t.Fatalf("pending credential rejected: %d %s", w.Code, w.Body.String())
	}
	ag, err := app.Store.Agent("a")
	if err != nil || !secure.EqualHash(ag.CredentialHash, secure.HashToken(next)) {
		t.Fatalf("old credential was not retired: %+v %v", ag, err)
	}
	req = httptest.NewRequest("GET", "/api/v1/agent/update-root", nil)
	req.Header.Set("Authorization", "Bearer "+old)
	req.Header.Set("X-Monik-Agent-Id", "a")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatalf("retired token still works: %d", w.Code)
	}
}

func TestTrustStageRejectsNonCertificate(t *testing.T) {
	app, _ := testApp(t)
	if err := app.Store.InsertAgent(&storage.AgentRow{ID: "a", Hostname: "h", DesiredConfig: "{}"}, "hash"); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	app.processSubmit(w, ownerRecent(t), protocol.SubmitOperation{
		Action: "trust.stage", ClientRequestKey: "trust-bad", TargetIDs: []string{"a"},
		Params: map[string]any{"trust_pem": "not-a-cert"},
	})
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d %s", w.Code, w.Body.String())
	}
}
