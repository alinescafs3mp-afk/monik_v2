package tufutil

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPublicationIsStableAndTamperingPreventsReuse(t *testing.T) {
	bundle := signBundle(t, t.TempDir(), t.TempDir(), "worker", "one")
	dest := t.TempDir()
	p, e := PreparePublication(bundle, dest, ImportOpts{Enroll: true}, HighWater{})
	if e != nil {
		t.Fatal(e)
	}
	q, e := PreparePublication(bundle, dest, ImportOpts{TrustedRoot: p.TrustedRoot}, p.HighWater)
	if e != nil || q.Result.ID != p.Result.ID {
		t.Fatal(q, e)
	}
	if e = os.WriteFile(p.Result.Artifacts[0].Path, []byte("broken"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = PreparePublication(bundle, dest, ImportOpts{TrustedRoot: p.TrustedRoot}, p.HighWater); e == nil {
		t.Fatal("corrupt object silently replaced")
	}
	b, _ := os.ReadFile(p.Result.Artifacts[0].Path)
	if string(b) != "broken" {
		t.Fatal("rewrote immutable object")
	}
	if _, e = LoadTrustedRoot(dest); !os.IsNotExist(e) {
		t.Fatal("prepare changed trust", e)
	}
}
func TestPublicationRejectsSymlinkStorage(t *testing.T) {
	dest := t.TempDir()
	if e := os.Symlink(t.TempDir(), filepath.Join(dest, "releases")); e != nil {
		t.Skip(e)
	}
	bundle := signBundle(t, t.TempDir(), t.TempDir(), "worker", "one")
	if _, e := PreparePublication(bundle, dest, ImportOpts{Enroll: true}, HighWater{}); e == nil {
		t.Fatal("linked storage accepted")
	}
}
func TestPublicationRejectsExpiredAndForeignMetadata(t *testing.T) {
	keys, repo, dest := t.TempDir(), t.TempDir(), t.TempDir()
	bundle := signBundle(t, keys, repo, "worker", "one")
	p, e := PreparePublication(bundle, dest, ImportOpts{Enroll: true}, HighWater{})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = PreparePublication(bundle, dest, ImportOpts{TrustedRoot: p.TrustedRoot, Now: time.Now().Add(72 * time.Hour)}, p.HighWater); e == nil {
		t.Fatal("expired import")
	}
	other := signBundle(t, t.TempDir(), t.TempDir(), "foreign", "other")
	if _, e = PreparePublication(other, dest, ImportOpts{TrustedRoot: p.TrustedRoot, Enroll: true}, p.HighWater); e == nil {
		t.Fatal("foreign root")
	}
}
func TestAudit10HighWaterObjectMustBeComplete(t *testing.T) {
	for _, raw := range []string{" null ", "{}", `{"root":1}`, `{"root":1,"timestamp":1,"snapshot":1,"targets":null}`, `{"root":1,"timestamp":1,"snapshot":1,"targets":1,"future":1}`} {
		if _, e := DecodeHighWater([]byte(raw)); e == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	b, _ := json.Marshal(HighWater{Root: 1, Timestamp: 2, Snapshot: 3, Targets: 4})
	if _, e := DecodeHighWater(b); e != nil {
		t.Fatal(e)
	}
}
func TestPublicationRejectsUnboundedEmptyEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.tgz")
	f, _ := os.Create(path)
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for i := 0; i < 513; i++ {
		tw.WriteHeader(&tar.Header{Name: "empty/", Mode: 0700, Typeflag: tar.TypeDir})
	}
	tw.Close()
	gz.Close()
	f.Close()
	if _, e := extractBundle(path); e == nil || !strings.Contains(e.Error(), "too many") {
		t.Fatalf("%v", e)
	}
}
func TestPublicationTargetPaths(t *testing.T) {
	for _, n := range []string{"../root.json", "linux-amd64//monik-agent", "/monik-agent", "C:/agent", "a/%2e%2e/agent", "a\\agent", "a/./agent", "a/../agent", ""} {
		if ValidTargetName(n) {
			t.Errorf("allowed %q", n)
		}
	}
	if !ValidTargetName("windows-amd64/monik-agent.exe") {
		t.Fatal("regular target rejected")
	}
}

func TestAudit10SigningKeyWithCorruptPublicHalfIsNotReplaced(t *testing.T) {
	dir := t.TempDir()
	ks, e := InitKeys(dir)
	if e != nil {
		t.Fatal(e)
	}
	damaged := append([]byte{}, ks.Root...)
	damaged[len(damaged)-1] ^= 1
	path := filepath.Join(dir, "root.ed25519")
	if e = os.WriteFile(path, damaged, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = InitKeys(dir); e == nil {
		t.Fatal("accepted inconsistent ed25519 key")
	}
	got, _ := os.ReadFile(path)
	if string(got) != string(damaged) {
		t.Fatal("rewrote damaged key")
	}
}
