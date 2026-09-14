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

func TestAudit8ManagedWorkerLivesInWritablePrivateSlot(t *testing.T) {
	root, src := t.TempDir(), t.TempDir()
	h, w := filepath.Join(src, "host"), filepath.Join(src, "worker")
	os.WriteFile(h, []byte("supervisor"), 0755)
	os.WriteFile(w, []byte("worker"), 0755)
	o := Options{Prefix: filepath.Join(root, "prefix"), StateDir: filepath.Join(root, "state"), UnitPath: filepath.Join(root, "monik-agent.service"), HostSrc: h, WorkerSrc: w, SkipSystemctl: true}
	if e := InstallLinux(o); e != nil {
		t.Fatal(e)
	}
	if e := InstallLinux(o); e != nil {
		t.Fatal("idempotent reinstall", e)
	}
	unit, e := os.ReadFile(o.UnitPath)
	if e != nil {
		t.Fatal(e)
	}
	for _, text := range []string{filepath.Join(o.StateDir, "bin", "monik-agent"), "User=monik", "UMask=0077", "KillMode=control-group"} {
		if !strings.Contains(string(unit), text) {
			t.Fatalf("missing %q", text)
		}
	}
	b, e := os.ReadFile(filepath.Join(o.StateDir, "bin", "monik-agent"))
	if e != nil || string(b) != "worker" {
		t.Fatal(string(b), e)
	}
}
func TestAudit8InstallerRejectsUnsafePathsAndAccount(t *testing.T) {
	for _, p := range []string{"/", "/var/lib", "/etc", "/usr/lib", "/home", "/tmp"} {
		if !unsafeInstallDir(p) {
			t.Fatal(p)
		}
	}
	o := Options{Prefix: "/usr/lib/monik", StateDir: "/var/lib/monik-agent", HostSrc: "host", WorkerSrc: "worker", User: "--help", SkipSystemctl: true}
	if e := InstallLinux(o); e == nil {
		t.Fatal("option-like username accepted")
	}
	o.User = "monik"
	o.StateDir = o.Prefix + "/state"
	if e := InstallLinux(o); e == nil {
		t.Fatal("overlapping install directories")
	}
}
func TestAudit8StateLinksAreRejectedWithoutChangingTarget(t *testing.T) {
	dir, out := t.TempDir(), filepath.Join(t.TempDir(), "unrelated")
	os.WriteFile(out, []byte("unchanged"), 0644)
	// WriteFile honors umask (this host is 0077). Pin the mode so the
	// assertion actually proves inspectPrivateState did not chmod the target.
	if e := os.Chmod(out, 0644); e != nil {
		t.Fatal(e)
	}
	os.Symlink(out, filepath.Join(dir, "link"))
	if e := inspectPrivateState(dir); e == nil {
		t.Fatal("symlink accepted")
	}
	os.Remove(filepath.Join(dir, "link"))
	if e := os.Link(out, filepath.Join(dir, "hardlink")); e != nil {
		t.Fatal(e)
	}
	if e := inspectPrivateState(dir); e == nil {
		t.Fatal("hard link accepted")
	}
	b, _ := os.ReadFile(out)
	i, _ := os.Stat(out)
	if string(b) != "unchanged" || i.Mode().Perm() != 0644 {
		t.Fatal("unrelated data changed")
	}
}
func TestAudit8PrivatePublicationDoesNotFollowEscapingDirectory(t *testing.T) {
	root, out := t.TempDir(), t.TempDir()
	if e := os.Symlink(out, filepath.Join(root, "bin")); e != nil {
		t.Fatal(e)
	}
	if e := writePrivateState(root, "bin/monik-agent", []byte("replacement"), 0755); e == nil {
		t.Fatal("escaped private state")
	}
	if _, e := os.Stat(filepath.Join(out, "monik-agent")); !os.IsNotExist(e) {
		t.Fatal("wrote outside private state", e)
	}
}
