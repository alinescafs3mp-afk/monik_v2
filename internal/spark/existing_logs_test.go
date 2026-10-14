//go:build linux

package spark

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func existingLogsFixture(t *testing.T, engine, actions string) (string, string, uint32) {
	t.Helper()
	// /tmp is deliberately rejected by production ancestor protection. Build
	// private fixtures below the protected local cache instead of weakening it.
	cache, e := os.UserCacheDir()
	if e != nil {
		t.Fatal(e)
	}
	base, e := os.MkdirTemp(cache, ".existing-log-fixture-")
	if e != nil {
		t.Fatal(e)
	}
	base, e = filepath.Abs(base)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	id := existingIDs[0]
	if e = os.Mkdir(filepath.Join(base, id), 0700); e != nil {
		t.Fatal(e)
	}
	for name, value := range map[string]string{"engine": engine, "actions": actions} {
		if e = os.WriteFile(filepath.Join(base, id, name+".log"), []byte(value), 0600); e != nil {
			t.Fatal(e)
		}
	}
	return base, id, uint32(os.Geteuid())
}
func startExistingFixtureReader(t *testing.T, root, id string, owner uint32, guard func() error) (LogReader, <-chan string, <-chan error) {
	t.Helper()
	ctx, end := context.WithCancel(context.Background())
	t.Cleanup(end)
	r, e := newExistingFileLogReader(ctx, root, id, owner, time.Millisecond, guard)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = r.Close() })
	lines := make(chan string, 1024)
	done := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(r)
		scanner.Buffer(make([]byte, 4096), LogMaxLine+100)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
		done <- scanner.Err()
		close(lines)
	}()
	return r, lines, done
}
func waitExistingLine(t *testing.T, lines <-chan string, contains string) string {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case line, ok := <-lines:
			if !ok {
				t.Fatalf("stream closed before %q", contains)
			}
			if strings.Contains(line, contains) {
				return line
			}
		case <-deadline:
			t.Fatalf("missing %q", contains)
		}
	}
}
func appendExistingFixture(t *testing.T, path, text string) {
	t.Helper()
	f, e := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if e != nil {
		t.Fatal(e)
	}
	_, e = f.WriteString(text)
	_ = f.Close()
	if e != nil {
		t.Fatal(e)
	}
}

func TestExistingLogsFixedLocalApprovalAndDefaultDisabled(t *testing.T) {
	_, _, p := existingFixture(t)
	if e := p.validateExistingCoordinator(); e != nil {
		t.Fatal(e)
	}
	if _, e := (LocalSupervisor{}).Logs(context.Background(), p); e == nil {
		t.Fatal("default logs enabled")
	}
	p.LogEnabled = true
	if p.validateExistingCoordinator() == nil {
		t.Fatal("logs accepted without pinned values")
	}
	a := p.Target.Existing
	a.LogRoot = existingLogRoot
	a.RedactionFile = existingLogRedactionFile
	a.RedactionSHA256 = strings.Repeat("b", 64)
	p.Target.DefinitionHash = digest(a)
	if e := p.validateExistingCoordinator(); e != nil {
		t.Fatal(e)
	}
	for _, change := range []func(*Profile){
		func(p *Profile) { p.Target.Existing.LogRoot = "/var/log" },
		func(p *Profile) { p.Target.Existing.RedactionFile = "/etc/spark-sglang-api.env" },
		func(p *Profile) { p.Target.Existing.RedactionSHA256 = "" },
		func(p *Profile) { p.RedactionFiles = []string{"/etc/secret"} },
		func(p *Profile) { p.Target.Unit = "arbitrary.service" },
		func(p *Profile) { p.ID = "../../secret" },
		func(p *Profile) { p.LogEnabled = false },
	} {
		q := p
		ac := *a
		q.Target.Existing = &ac
		change(&q)
		q.Target.DefinitionHash = digest(q.Target.Existing)
		if q.validateExistingCoordinator() == nil {
			t.Fatal("unapproved log policy accepted")
		}
	}
	root, id, owner := existingLogsFixture(t, "", "")
	for _, bad := range []string{"../" + id, "spark-sglang.service", "qwen-unapproved", "*"} {
		if r, e := newExistingFileLogReader(context.Background(), root, bad, owner, time.Millisecond, nil); e == nil {
			_ = r.Close()
			t.Fatal("arbitrary ID accepted")
		}
	}
}

func TestExistingLogsEveryFixedModelUsesOnlyItsTwoFiles(t *testing.T) {
	root, first, owner := existingLogsFixture(t, "fixed engine\n", "fixed actions\n")
	for _, id := range existingIDs {
		if id != first {
			if e := os.Mkdir(filepath.Join(root, id), 0700); e != nil {
				t.Fatal(e)
			}
			for _, name := range []string{"engine", "actions"} {
				if e := os.WriteFile(filepath.Join(root, id, name+".log"), []byte("fixed "+name+"\n"), 0600); e != nil {
					t.Fatal(e)
				}
			}
		}
		r, lines, _ := startExistingFixtureReader(t, root, id, owner, nil)
		waitExistingLine(t, lines, "fixed engine")
		waitExistingLine(t, lines, "fixed actions")
		_ = r.Close()
	}
}

func TestExistingPinnedRedactorValuesAndChunkedSourceIsolation(t *testing.T) {
	root, id, owner := existingLogsFixture(t, "engine naked-secret-", "action safe\n")
	path := filepath.Join(root, "values")
	values := []byte("naked-secret-ABC\nplain-key-without-prefix\n")
	if e := os.WriteFile(path, values, 0600); e != nil {
		t.Fatal(e)
	}
	sum := sha256.Sum256(values)
	pin := hex.EncodeToString(sum[:])
	redactor, e := pinnedExistingLogRedactor(path, pin, owner)
	if e != nil {
		t.Fatal(e)
	}
	r, lines, _ := startExistingFixtureReader(t, root, id, owner, nil)
	waitExistingLine(t, lines, "action safe")
	appendExistingFixture(t, filepath.Join(root, id, "engine.log"), "ABC plain-key-without-prefix authorization: Bearer example\n")
	line := waitExistingLine(t, lines, "engine naked")
	clean := redactor.Clean([]byte(line))
	if strings.Contains(clean, "naked-secret") || strings.Contains(clean, "plain-key") || strings.Contains(clean, "example") {
		t.Fatalf("secret escaped: %q", clean)
	}
	if !strings.Contains(clean, "[redacted]") {
		t.Fatal(clean)
	}
	_ = r.Close()
	if _, e = pinnedExistingLogRedactor(path, strings.Repeat("a", 64), owner); e == nil {
		t.Fatal("wrong pin accepted")
	}
	for _, value := range []string{"", "   \n", "secret\r\n", "secret\x00\n", strings.Repeat("x", LogMaxLine+1) + "\n", strings.Repeat("x", (64<<10)+1)} {
		if e = os.WriteFile(path, []byte(value), 0600); e != nil {
			t.Fatal(e)
		}
		sum = sha256.Sum256([]byte(value))
		if _, e = pinnedExistingLogRedactor(path, hex.EncodeToString(sum[:]), owner); e == nil {
			t.Fatal("unsafe values accepted")
		}
	}
}

func TestExistingLogsTailBothSourcesAndOversizeBounds(t *testing.T) {
	var engine, actions strings.Builder
	for i := 0; i < 600; i++ {
		fmt.Fprintf(&engine, "engine-%04d %s\n", i, strings.Repeat("x", 500))
		fmt.Fprintf(&actions, "action-%04d %s\n", i, strings.Repeat("y", 500))
	}
	root, id, owner := existingLogsFixture(t, engine.String(), actions.String())
	r, lines, _ := startExistingFixtureReader(t, root, id, owner, nil)
	got := []string{}
	bytes := 0
	for len(got) < 200 {
		select {
		case line := <-lines:
			got = append(got, line)
			bytes += len(line) + 1
		case <-time.After(time.Second):
			t.Fatal("initial tail incomplete")
		}
	}
	if bytes > 128<<10 {
		t.Fatal("tail budget exceeded", bytes)
	}
	select {
	case line := <-lines:
		t.Fatal("more than 200 initial records", line)
	case <-time.After(20 * time.Millisecond):
	}
	appendExistingFixture(t, filepath.Join(root, id, "engine.log"), strings.Repeat("z", LogMaxLine+1)+"\nvisible\n")
	waitExistingLine(t, lines, "oversized log record dropped")
	waitExistingLine(t, lines, "visible")
	_ = r.Close()
}

func TestExistingLogsRotationDropsPartialAndFollowsExactReplacement(t *testing.T) {
	root, id, owner := existingLogsFixture(t, "partial-secret-no-newline", "ready\n")
	r, lines, _ := startExistingFixtureReader(t, root, id, owner, nil)
	waitExistingLine(t, lines, "ready")
	path := filepath.Join(root, id, "engine.log")
	if e := os.Rename(path, path+".1"); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(path, []byte("replacement\n"), 0600); e != nil {
		t.Fatal(e)
	}
	for _, want := range []string{"incomplete log record dropped", "rotated or truncated", "replacement"} {
		line := waitExistingLine(t, lines, want)
		if strings.Contains(line, "partial-secret") {
			t.Fatal("partial escaped")
		}
	}
	if e := os.WriteFile(path, []byte("short\n"), 0600); e != nil {
		t.Fatal(e)
	}
	waitExistingLine(t, lines, "rotated or truncated")
	waitExistingLine(t, lines, "short")
	_ = r.Close()
}

func TestExistingLogsRejectSymlinkPermissionsOwnersHardlinksAndFIFO(t *testing.T) {
	for _, kind := range []string{"leaf-symlink", "ancestor-symlink", "file-write", "ancestor-write", "hardlink", "fifo", "wrong-owner"} {
		t.Run(kind, func(t *testing.T) {
			root, id, owner := existingLogsFixture(t, "safe\n", "safe\n")
			path := filepath.Join(root, id, "engine.log")
			switch kind {
			case "leaf-symlink":
				_ = os.Rename(path, path+".old")
				if e := os.Symlink(path+".old", path); e != nil {
					t.Fatal(e)
				}
			case "ancestor-symlink":
				dir := filepath.Join(root, id)
				_ = os.Rename(dir, dir+".old")
				if e := os.Symlink(dir+".old", dir); e != nil {
					t.Fatal(e)
				}
			case "file-write":
				_ = os.Chmod(path, 0620)
			case "ancestor-write":
				_ = os.Chmod(filepath.Join(root, id), 0720)
			case "hardlink":
				if e := os.Link(path, path+".link"); e != nil {
					t.Fatal(e)
				}
			case "fifo":
				_ = os.Remove(path)
				if e := unix.Mkfifo(path, 0600); e != nil {
					t.Fatal(e)
				}
			case "wrong-owner":
				owner++
			}
			if r, e := newExistingFileLogReader(context.Background(), root, id, owner, time.Millisecond, nil); e == nil {
				_ = r.Close()
				t.Fatal("unsafe source opened")
			}
		})
	}
}

func TestExistingLogsCloseOnPermissionSymlinkAndRedactionDrift(t *testing.T) {
	for _, kind := range []string{"permission", "symlink", "redaction-replace", "redaction-mode"} {
		t.Run(kind, func(t *testing.T) {
			root, id, owner := existingLogsFixture(t, "engine\n", "actions\n")
			values := filepath.Join(root, "values")
			_ = os.WriteFile(values, []byte("secret\n"), 0600)
			valueHash := sha256.Sum256([]byte("secret\n"))
			redactor, e := pinnedExistingLogRedactor(values, hex.EncodeToString(valueHash[:]), owner)
			if e != nil {
				t.Fatal(e)
			}
			guard := redactor.existingGuard
			r, lines, done := startExistingFixtureReader(t, root, id, owner, guard)
			waitExistingLine(t, lines, "actions")
			path := filepath.Join(root, id, "engine.log")
			switch kind {
			case "permission":
				_ = os.Chmod(path, 0660)
			case "symlink":
				_ = os.Rename(path, path+".old")
				_ = os.Symlink(path+".old", path)
			case "redaction-replace":
				_ = os.Rename(values, values+".old")
				_ = os.WriteFile(values, []byte("secret\n"), 0600)
			case "redaction-mode":
				_ = os.Chmod(values, 0644)
			}
			select {
			case e := <-done:
				if e == nil {
					t.Fatal("drift closed without failure")
				}
			case <-time.After(time.Second):
				t.Fatal("drift did not revoke stream")
			}
			_ = r.Close()
		})
	}
}

type existingLogsFixtureSupervisor struct {
	fakeSupervisor
	root, id string
	owner    uint32
	mu       sync.Mutex
	calls    int
	closed   chan struct{}
}

func (s *existingLogsFixtureSupervisor) Logs(ctx context.Context, _ Profile) (LogReader, error) {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	r, e := newExistingFileLogReader(ctx, s.root, s.id, s.owner, time.Millisecond, nil)
	if e != nil {
		return nil, e
	}
	return &existingCloseWitness{LogReader: r, closed: s.closed}, nil
}

type existingCloseWitness struct {
	LogReader
	once   sync.Once
	closed chan struct{}
}

func (r *existingCloseWitness) Close() error {
	e := r.LogReader.Close()
	r.once.Do(func() { close(r.closed) })
	return e
}

func TestExistingLogsSharedHubRevocationAndFourUpstreamBounds(t *testing.T) {
	root, id, owner := existingLogsFixture(t, "authorization: Bearer hidden\n", "actions\n")
	s := &existingLogsFixtureSupervisor{root: root, id: id, owner: owner, closed: make(chan struct{})}
	h := NewLogHub()
	_, _, p := controlFixture(t)
	p.LogEnabled = true
	ctx, end := context.WithCancel(context.Background())
	defer end()
	a, e := h.Subscribe(ctx, p, s)
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	b, e := h.Subscribe(ctx, p, s)
	if e != nil {
		t.Fatal(e)
	}
	defer b.Close()
	select {
	case record := <-a.Records:
		if strings.Contains(record.Text, "hidden") {
			t.Fatal("unredacted transport record")
		}
	case <-time.After(time.Second):
		t.Fatal("source missing")
	}
	s.mu.Lock()
	calls := s.calls
	s.mu.Unlock()
	if calls != 1 {
		t.Fatal("viewers multiplied file streams")
	}
	h.CloseProfile(p.ID, "lease_revoked")
	select {
	case <-s.closed:
	case <-time.After(time.Second):
		t.Fatal("revocation did not close file reader")
	}
	// All four profile buffers share one host bound; the fifth source is denied.
	h = NewLogHub()
	var subs []LogSubscription
	for i := 0; i < 4; i++ {
		q := p
		q.ID = fmt.Sprintf("logs-%d", i)
		sub, e := h.Subscribe(ctx, q, &fakeSupervisor{})
		if e != nil {
			t.Fatal(e)
		}
		subs = append(subs, sub)
	}
	q := p
	q.ID = "logs-5"
	if sub, e := h.Subscribe(ctx, q, &fakeSupervisor{}); e == nil {
		sub.Close()
		t.Fatal("fifth upstream allowed")
	}
	for _, sub := range subs {
		sub.Close()
	}
}

var _ io.ReadCloser = (*existingFileLogReader)(nil)
