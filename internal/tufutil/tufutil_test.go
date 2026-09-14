package tufutil

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func signBundle(t *testing.T, keysDir, repo, payload, version string) string {
	t.Helper()
	ks, err := InitKeys(keysDir)
	if err != nil {
		t.Fatal(err)
	}
	art := filepath.Join(t.TempDir(), "linux-amd64-monik-agent")
	if err := os.WriteFile(art, []byte(payload), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := SignRepository(ks, repo, map[string]string{"linux-amd64/monik-agent": art}, 30, 2); err != nil {
		t.Fatal(err)
	}
	bundle := filepath.Join(t.TempDir(), version+".tgz")
	if err := PackBundle(repo, bundle, version, "test"); err != nil {
		t.Fatal(err)
	}
	return bundle
}

func TestSignVerifyPackImport(t *testing.T) {
	keysDir := t.TempDir()
	repo := t.TempDir()
	bundle := signBundle(t, keysDir, repo, "agent-bin", "0.1.0")
	if _, _, err := VerifyLocal(repo, "linux-amd64/monik-agent"); err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	res, err := ImportBundle(bundle, dest)
	if err != nil {
		t.Fatal(err)
	}
	if res.Version != "0.1.0" || len(res.Artifacts) != 1 {
		t.Fatalf("%+v", res)
	}
}

func TestImportTrustedEnrollsOnceAndRejectsForeignRoot(t *testing.T) {
	keysA := t.TempDir()
	repoA := t.TempDir()
	bundleA := signBundle(t, keysA, repoA, "agent-a", "0.1.0")
	dest := t.TempDir()
	if _, err := ImportTrusted(bundleA, dest, ImportOpts{}); err == nil {
		t.Fatal("import without enrolled root must fail")
	}
	res, err := ImportTrusted(bundleA, dest, ImportOpts{Enroll: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Version != "0.1.0" {
		t.Fatalf("%+v", res)
	}
	if _, err := LoadTrustedRoot(dest); err != nil {
		t.Fatal(err)
	}
	bundleA2 := signBundle(t, keysA, repoA, "agent-a2", "0.1.1")
	if _, err := ImportTrusted(bundleA2, dest, ImportOpts{}); err != nil {
		t.Fatal(err)
	}
	keysB := t.TempDir()
	repoB := t.TempDir()
	bundleB := signBundle(t, keysB, repoB, "evil", "9.9.9")
	if _, err := ImportTrusted(bundleB, dest, ImportOpts{Enroll: true}); err == nil {
		t.Fatal("foreign root must not replace enrolled trust")
	}
}

func TestImportTrustedRejectsExpiredTimestamp(t *testing.T) {
	keysDir := t.TempDir()
	repo := t.TempDir()
	ks, err := InitKeys(keysDir)
	if err != nil {
		t.Fatal(err)
	}
	art := filepath.Join(t.TempDir(), "bin")
	if err := os.WriteFile(art, []byte("bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := SignRepository(ks, repo, map[string]string{"linux-amd64/monik-agent": art}, 30, 2); err != nil {
		t.Fatal(err)
	}
	bundle := filepath.Join(t.TempDir(), "rel.tgz")
	if err := PackBundle(repo, bundle, "0.1.0", ""); err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	if _, err := ImportTrusted(bundle, dest, ImportOpts{Enroll: true, Now: time.Now().Add(72 * time.Hour)}); err == nil {
		t.Fatal("expired timestamp accepted")
	}
}

func TestReviewMixedSignedMetadataIsRejected(t *testing.T) {
	keys, repo := t.TempDir(), t.TempDir()
	signBundle(t, keys, repo, "old", "one")
	oldTargets, _ := os.ReadFile(filepath.Join(repo, "targets.json"))
	signBundle(t, keys, repo, "new", "two")
	root, _ := os.ReadFile(filepath.Join(repo, "root.json"))
	os.WriteFile(filepath.Join(repo, "targets.json"), oldTargets, 0600)
	if _, err := VerifyRepo(root, repo, HighWater{}, time.Now()); err == nil {
		t.Fatal("a valid old targets signature must not satisfy a newer snapshot version")
	}
}

func TestReviewTargetBytesMustMatchSignedEntry(t *testing.T) {
	repo := t.TempDir()
	signBundle(t, t.TempDir(), repo, "authentic", "one")
	root, e := os.ReadFile(filepath.Join(repo, "root.json"))
	if e != nil {
		t.Fatal(e)
	}
	if _, e := VerifyRepo(root, repo, HighWater{}, time.Now()); e != nil {
		t.Fatal(e)
	}
	if e := VerifyTarget(repo, "linux-amd64/monik-agent", []byte("authentic")); e != nil {
		t.Fatal(e)
	}
	if e := VerifyTarget(repo, "linux-amd64/monik-agent", []byte("arbitrary")); e == nil {
		t.Fatal("command checksum replaced the signed target authority")
	}
	if e := VerifyTarget(repo, "linux-amd64/monik-service-host", []byte("authentic")); e == nil {
		t.Fatal("unsigned component")
	}
}
func TestReviewCorruptVersionJournalCannotResetRollbackProtection(t *testing.T) {
	dir := t.TempDir()
	if e := SaveHighWater(dir, HighWater{Targets: 42}); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(HighWaterPath(dir), []byte("broken"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := ReadHighWater(dir); e == nil {
		t.Fatal("corrupt highwater silently accepted")
	}
}

func TestReviewInvalidTargetCannotReplacePublishedRelease(t *testing.T) {
	keys, repo, dest := t.TempDir(), t.TempDir(), t.TempDir()
	good := signBundle(t, keys, repo, "good", "one")
	if _, e := ImportTrusted(good, dest, ImportOpts{Enroll: true}); e != nil {
		t.Fatal(e)
	}
	before, e := ReadHighWater(dest)
	if e != nil {
		t.Fatal(e)
	}
	signBundle(t, keys, repo, "next", "two")
	if e := os.WriteFile(filepath.Join(repo, "targets", "linux-amd64", "monik-agent"), []byte("tampered"), 0600); e != nil {
		t.Fatal(e)
	}
	bad := filepath.Join(t.TempDir(), "bad.tgz")
	if e := PackBundle(repo, bad, "two", ""); e != nil {
		t.Fatal(e)
	}
	if _, e := ImportTrusted(bad, dest, ImportOpts{}); e == nil {
		t.Fatal("invalid bytes accepted")
	}
	after, e := ReadHighWater(dest)
	if e != nil || after != before {
		t.Fatalf("versions advanced on invalid import: %+v %v", after, e)
	}
	raw, e := os.ReadFile(filepath.Join(dest, "targets", "linux-amd64", "monik-agent"))
	if e != nil || string(raw) != "good" {
		t.Fatalf("working target replaced: %s %v", raw, e)
	}
	empty := t.TempDir()
	if _, e := ImportTrusted(bad, empty, ImportOpts{Enroll: true}); e == nil {
		t.Fatal("invalid initial import accepted")
	}
	if _, e := LoadTrustedRoot(empty); !os.IsNotExist(e) {
		t.Fatalf("invalid import enrolled trust: %v", e)
	}
}
