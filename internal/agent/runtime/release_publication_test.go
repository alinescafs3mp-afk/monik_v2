package runtime

import (
	"net/http"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"
	"testing"

	"github.com/alinescafs3mp-afk/monik_v2/internal/secure"
	"github.com/alinescafs3mp-afk/monik_v2/internal/tufutil"
)

// Uses real signed repositories and HTTPS, but never runs the fixture binaries.
func TestAudit10AgentDownloadsExactPinnedRelease(t *testing.T) {
	keys, err := tufutil.InitKeys(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo, dest := t.TempDir(), t.TempDir()
	name := goruntime.GOOS + "-" + goruntime.GOARCH + "/monik-agent"
	if goruntime.GOOS == "windows" {
		name += ".exe"
	}
	var prior tufutil.HighWater
	var trusted []byte
	makePublication := func(body string) *tufutil.Publication {
		t.Helper()
		bin := filepath.Join(t.TempDir(), "fixture-worker")
		if e := os.WriteFile(bin, []byte(body), 0600); e != nil {
			t.Fatal(e)
		}
		if e := tufutil.SignRepository(keys, repo, map[string]string{name: bin}, 30, 2); e != nil {
			t.Fatal(e)
		}
		bundle := filepath.Join(t.TempDir(), "bundle.tgz")
		if e := tufutil.PackBundle(repo, bundle, body, "fixture"); e != nil {
			t.Fatal(e)
		}
		p, e := tufutil.PreparePublication(bundle, dest, tufutil.ImportOpts{Enroll: true, TrustedRoot: trusted}, prior)
		if e != nil {
			t.Fatal(e)
		}
		prior, trusted = p.HighWater, p.TrustedRoot
		return p
	}
	first := makePublication("release-A")
	second := makePublication("release-B")
	pubs := map[string]*tufutil.Publication{first.Result.ID: first, second.Result.ID: second}
	var mu sync.Mutex
	paths := []string{}
	a, _ := auditWorker(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer fixture-token" || r.Header.Get("X-Monik-Agent-Id") != "fixture" {
			w.WriteHeader(401)
			return
		}
		tail := strings.TrimPrefix(r.URL.Path, "/api/v1/agent/releases/")
		parts := strings.SplitN(tail, "/", 3)
		if len(parts) != 3 {
			w.WriteHeader(404)
			return
		}
		p, ok := pubs[parts[0]]
		if !ok {
			w.WriteHeader(404)
			return
		}
		dir, _ := tufutil.PublicationDir(dest, p.Result.ID)
		relative := parts[2]
		if parts[1] == "artifacts" {
			relative = "targets/" + relative
		} else if parts[1] != "tuf" {
			w.WriteHeader(404)
			return
		}
		b, e := os.ReadFile(filepath.Join(dir, relative))
		if e != nil {
			w.WriteHeader(404)
			return
		}
		w.Write(b)
	})
	if e := os.WriteFile(filepath.Join(a.State.File.StateDir, "tuf-root.json"), trusted, 0600); e != nil {
		t.Fatal(e)
	}
	out, e := a.downloadVerifiedRelease(name, secure.SHA256Bytes([]byte("release-A")), first.Result.ID)
	if e != nil {
		t.Fatal(e)
	}
	got, e := os.ReadFile(out)
	if e != nil || string(got) != "release-A" {
		t.Fatal(string(got), e)
	}
	out, e = a.downloadVerifiedRelease(name, secure.SHA256Bytes([]byte("release-B")), second.Result.ID)
	if e != nil {
		t.Fatal(e)
	}
	got, _ = os.ReadFile(out)
	if string(got) != "release-B" {
		t.Fatal(string(got))
	}
	if _, e = a.downloadVerifiedRelease(name, secure.SHA256Bytes([]byte("release-A")), first.Result.ID); e == nil {
		t.Fatal("accepted metadata rollback")
	}
	if e = os.WriteFile(second.Result.Artifacts[0].Path, []byte("tampered!"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = a.downloadVerifiedRelease(name, secure.SHA256Bytes([]byte("tampered!")), second.Result.ID); e == nil {
		t.Fatal("trusted command hash instead of signed target")
	}
	got, _ = os.ReadFile(out)
	if string(got) != "release-B" {
		t.Fatal("failed download overwrote prior staged worker")
	}
	mu.Lock()
	defer mu.Unlock()
	for _, p := range paths {
		if !strings.HasPrefix(p, "/api/v1/agent/releases/") {
			t.Fatalf("used mutable URL %s", p)
		}
	}
}
func TestAudit10InvalidPinnedDigestDoesNotFetch(t *testing.T) {
	calls := 0
	a, _ := auditWorker(t, func(http.ResponseWriter, *http.Request) { calls++ })
	if _, e := a.downloadVerifiedRelease("linux-amd64/monik-agent", "hash", "../../x"); e == nil {
		t.Fatal("bad digest accepted")
	}
	if calls != 0 {
		t.Fatal("unexpected network access")
	}
}
