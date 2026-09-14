package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallLinuxCopiesProtectedLayout(t *testing.T) {
	root := t.TempDir()
	src := t.TempDir()
	host := filepath.Join(src, "monik-service-host")
	worker := filepath.Join(src, "monik-agent")
	if err := os.WriteFile(host, []byte("host"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(worker, []byte("worker"), 0o755); err != nil {
		t.Fatal(err)
	}
	opts := Options{
		Prefix: filepath.Join(root, "usr/lib/monik"), StateDir: filepath.Join(root, "var/lib/monik-agent"),
		UnitPath: filepath.Join(root, "etc/systemd/system/monik-agent.service"), User: "monik",
		HostSrc: host, WorkerSrc: worker, ConfigPath: filepath.Join(root, "var/lib/monik-agent/agent.json"),
		SkipSystemctl: true,
	}
	if err := InstallLinux(opts); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(opts.Prefix, "monik-agent")); err != nil {
		t.Fatal(err)
	}
	unit, err := os.ReadFile(opts.UnitPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(unit), opts.Prefix) || !strings.Contains(string(unit), "NoNewPrivileges=true") {
		t.Fatalf("unit: %s", unit)
	}
}

func TestReviewInstallCopyDoesNotTruncateItself(t *testing.T) {
	p := filepath.Join(t.TempDir(), "agent")
	if err := os.WriteFile(p, []byte("working-binary"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(p, p, 0755); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if string(b) != "working-binary" {
		t.Fatal("self-install truncated binary")
	}
}
func TestReviewFailedCopyKeepsWorkingTarget(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "current")
	_ = os.WriteFile(dst, []byte("old"), 0755)
	if err := copyFile(filepath.Join(dir, "missing"), dst, 0755); err == nil {
		t.Fatal("missing source accepted")
	}
	b, _ := os.ReadFile(dst)
	if string(b) != "old" {
		t.Fatal("failed copy destroyed current")
	}
}
