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
