package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"github.com/alinescafs3mp-afk/monik_v2/internal/secure"
	"github.com/alinescafs3mp-afk/monik_v2/internal/storage"
	"github.com/alinescafs3mp-afk/monik_v2/internal/tufutil"
)

func releaseBundle10(t *testing.T, keys, repo, payload, version string) string {
	t.Helper()
	ks, e := tufutil.InitKeys(keys)
	if e != nil {
		t.Fatal(e)
	}
	bin := filepath.Join(t.TempDir(), "worker")
	if e = os.WriteFile(bin, []byte(payload), 0700); e != nil {
		t.Fatal(e)
	}
	if e = tufutil.SignRepository(ks, repo, map[string]string{"linux-amd64/monik-agent": bin}, 30, 2); e != nil {
		t.Fatal(e)
	}
	bundle := filepath.Join(t.TempDir(), "release.tgz")
	if e = tufutil.PackBundle(repo, bundle, version, "audit fixture"); e != nil {
		t.Fatal(e)
	}
	return bundle
}
func importRelease10(t *testing.T, a *App, bundle, key string) protocol.Operation {
	t.Helper()
	w := httptest.NewRecorder()
	a.processSubmit(w, ownerRecent(t), protocol.SubmitOperation{Action: "update.import", ClientRequestKey: key, Params: map[string]any{"bundle_path": bundle, "enroll_root": true}})
	var op protocol.Operation
	if w.Code >= 400 || json.Unmarshal(w.Body.Bytes(), &op) != nil {
		t.Fatalf("import %d: %s", w.Code, w.Body.String())
	}
	return op
}
func TestAudit10ImportPreservesOldArtifact(t *testing.T) {
	a, _ := testApp(t)
	keys, repo := t.TempDir(), t.TempDir()
	op := importRelease10(t, a, releaseBundle10(t, keys, repo, "first-worker", "1.0"), "first")
	if op.Status != protocol.OpCompleted {
		t.Fatalf("first import: %+v", op)
	}
	rels, e := a.Store.Releases()
	if e != nil || len(rels) != 1 {
		t.Fatal(rels, e)
	}
	arts, e := a.Store.ArtifactsByRelease(rels[0]["id"].(string))
	if e != nil || len(arts) != 1 {
		t.Fatal(arts, e)
	}
	firstPath := arts[0]["path"].(string)
	op = importRelease10(t, a, releaseBundle10(t, keys, repo, "second-worker", "1.1"), "second")
	if op.Status != protocol.OpCompleted {
		t.Fatalf("second import: %+v", op)
	}
	raw, e := os.ReadFile(firstPath)
	if e != nil || string(raw) != "first-worker" {
		t.Fatalf("old release path was replaced: %q %v", raw, e)
	}
}
func TestAudit10CatalogRejectsPartialArtifactCommit(t *testing.T) {
	a, _ := testApp(t)
	_, e := a.Store.DB.Exec(`CREATE TRIGGER reject_artifact BEFORE INSERT ON release_artifacts BEGIN SELECT RAISE(ABORT,'test artifact failure'); END;`)
	if e != nil {
		t.Fatal(e)
	}
	op := importRelease10(t, a, releaseBundle10(t, t.TempDir(), t.TempDir(), "worker", "1.0"), "fail")
	rels, e := a.Store.Releases()
	if e != nil {
		t.Fatal(e)
	}
	if len(rels) != 0 || op.Status == protocol.OpCompleted {
		t.Fatalf("partial catalog/success on failed artifact insert: releases=%d status=%s", len(rels), op.Status)
	}
}

func TestAudit10FailedImportKeepsTrustAndCanBeRetried(t *testing.T) {
	a, _ := testApp(t)
	bundle := releaseBundle10(t, t.TempDir(), t.TempDir(), "worker", "one")
	if _, e := a.Store.DB.Exec(`CREATE TRIGGER reject_publish BEFORE INSERT ON release_artifacts BEGIN SELECT RAISE(ABORT,'injected failure'); END;`); e != nil {
		t.Fatal(e)
	}
	op := importRelease10(t, a, bundle, "bad")
	if op.Status == protocol.OpCompleted {
		t.Fatal("reported success")
	}
	if _, e := a.Store.ReleaseTrust(); !errors.Is(e, storage.ErrNotFound) {
		t.Fatalf("failed publication changed trust: %v", e)
	}
	_, _ = a.Store.DB.Exec(`DROP TRIGGER reject_publish`)
	op = importRelease10(t, a, bundle, "retry")
	if op.Status != protocol.OpCompleted {
		t.Fatalf("retry: %+v", op)
	}
	rels, e := a.Store.Releases()
	if e != nil || len(rels) != 1 {
		t.Fatal(rels, e)
	}
	op = importRelease10(t, a, bundle, "repeat")
	rels, e = a.Store.Releases()
	if e != nil || len(rels) != 1 || op.Status != protocol.OpCompleted {
		t.Fatal(rels, e, op.Status)
	}
}
func TestAudit10CatalogOutcomeIsAtomic(t *testing.T) {
	a, _ := testApp(t)
	_, e := a.Store.DB.Exec(`CREATE TRIGGER reject_success BEFORE UPDATE ON operation_targets WHEN NEW.status='succeeded' BEGIN SELECT RAISE(ABORT,'injected result failure'); END;`)
	if e != nil {
		t.Fatal(e)
	}
	op := importRelease10(t, a, releaseBundle10(t, t.TempDir(), t.TempDir(), "worker", "one"), "result-fail")
	rels, e := a.Store.Releases()
	if e != nil || len(rels) != 0 || op.Status == protocol.OpCompleted {
		t.Fatal(rels, e, op.Status)
	}
	if _, e = a.Store.ReleaseTrust(); !errors.Is(e, storage.ErrNotFound) {
		t.Fatal("trust survived failed result commit", e)
	}
}
func TestAudit10PinnedHTTPFilesSurviveLaterImport(t *testing.T) {
	a, h := testApp(t)
	keys, repo := t.TempDir(), t.TempDir()
	op := importRelease10(t, a, releaseBundle10(t, keys, repo, "old-worker", "one"), "A")
	if op.Status != protocol.OpCompleted {
		t.Fatal(op.Status)
	}
	rels, _ := a.Store.Releases()
	id := rels[0]["id"].(string)
	var before []byte
	if e := a.Store.InsertAgent(&storage.AgentRow{ID: "download-agent", Hostname: "fixture", OS: "linux", Arch: "amd64", DesiredConfig: "{}"}, secure.HashToken("token")); e != nil {
		t.Fatal(e)
	}
	get := func(path string, auth bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		if auth {
			r.Header.Set("Authorization", "Bearer token")
			r.Header.Set("X-Monik-Agent-Id", "download-agent")
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	prefix := "/api/v1/agent/releases/" + id + "/"
	w := get(prefix+"tuf/targets.json", true)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	before = append(before, w.Body.Bytes()...)
	op = importRelease10(t, a, releaseBundle10(t, keys, repo, "new-worker", "two"), "B")
	if op.Status != protocol.OpCompleted {
		t.Fatal(op.Status)
	}
	w = get(prefix+"tuf/targets.json", true)
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), before) {
		t.Fatal("metadata changed", w.Code)
	}
	w = get(prefix+"artifacts/linux-amd64/monik-agent", true)
	if w.Code != 200 || w.Body.String() != "old-worker" {
		t.Fatal(w.Code, w.Body.String())
	}
	if get(prefix+"artifacts/linux-amd64/monik-agent", false).Code != 401 {
		t.Fatal("anonymous target download")
	}
	if get(prefix+"tuf/manifest.json", true).Code != 404 {
		t.Fatal("extra metadata exposed")
	}
	if get(prefix+"artifacts/linux-amd64/not-signed", true).Code != 404 {
		t.Fatal("unlisted target")
	}
	if get("/api/v1/agent/tuf/", true).Code == 200 {
		t.Fatal("legacy metadata directory listing exposed")
	}
}
func TestAudit10RolloutPinsCompleteJobAndRejectsLegacyAgent(t *testing.T) {
	a, _ := testApp(t)
	op := importRelease10(t, a, releaseBundle10(t, t.TempDir(), t.TempDir(), "worker", "one"), "import")
	if op.Status != protocol.OpCompleted {
		t.Fatal(op.Status)
	}
	rels, _ := a.Store.Releases()
	id := rels[0]["id"].(string)
	for _, name := range []string{"modern", "old"} {
		if e := a.Store.InsertAgent(&storage.AgentRow{ID: name, Hostname: name, OS: "linux", Arch: "amd64", ManagedReady: true, DesiredConfig: "{}"}, "hash"); e != nil {
			t.Fatal(e)
		}
		caps := "{}"
		if name == "modern" {
			caps = `{"immutable_release_v1":{"status":"supported"}}`
		}
		if _, e := a.Store.DB.Exec(`UPDATE agents SET managed_ready=1,capabilities=?,last_live_at=? WHERE id=?`, caps, time.Now().UTC().Format(time.RFC3339Nano), name); e != nil {
			t.Fatal(e)
		}
	}
	w := httptest.NewRecorder()
	a.processSubmit(w, ownerRecent(t), protocol.SubmitOperation{Action: "update.rollout", ClientRequestKey: "roll", TargetIDs: []string{"modern", "old"}, Params: map[string]any{"release_id": id}})
	if w.Code >= 400 {
		t.Fatal(w.Code, w.Body.String())
	}
	jobs, e := a.Store.PendingJobs("modern", 5)
	if e != nil || len(jobs) != 1 || jobs[0].Params["release_digest"] != id || jobs[0].Params["sha256"] == nil {
		t.Fatal(jobs, e)
	}
	jobs, e = a.Store.PendingJobs("old", 5)
	if e != nil || len(jobs) != 0 {
		t.Fatal(jobs, e)
	}
}
func TestAudit10LegacyTrustIsPreserved(t *testing.T) {
	a, _ := testApp(t)
	keys, repo := t.TempDir(), t.TempDir()
	rootDir := filepath.Join(a.Cfg.DataDir, "tuf")
	old := releaseBundle10(t, keys, repo, "legacy-worker", "old")
	if _, e := tufutil.ImportTrusted(old, rootDir, tufutil.ImportOpts{Enroll: true}); e != nil {
		t.Fatal(e)
	}
	before, e := os.ReadFile(filepath.Join(rootDir, "repository", "targets.json"))
	if e != nil {
		t.Fatal(e)
	}
	op := importRelease10(t, a, releaseBundle10(t, keys, repo, "modern-worker", "new"), "modern")
	if op.Status != protocol.OpCompleted {
		t.Fatal(op.Status)
	}
	after, e := os.ReadFile(filepath.Join(rootDir, "repository", "targets.json"))
	if e != nil || !bytes.Equal(before, after) {
		t.Fatal("legacy metadata overwritten", e)
	}
	payload, e := os.ReadFile(filepath.Join(rootDir, "targets", "linux-amd64/monik-agent"))
	if e != nil || string(payload) != "legacy-worker" {
		t.Fatal("legacy target overwritten", e)
	}
}
func TestAudit10CatalogueReadErrorIsNotEmptySuccess(t *testing.T) {
	a, _ := testApp(t)
	_, _ = a.Store.DB.Exec("DROP TABLE release_publications")
	w := httptest.NewRecorder()
	a.handleReleases(w, httptest.NewRequest("GET", "/api/v1/releases", nil), ownerRecent(t))
	if w.Code != 500 {
		t.Fatalf("got %d", w.Code)
	}
}
