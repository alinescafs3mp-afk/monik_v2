//go:build linux

package install

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestV18SystemdUnitSyntax(t *testing.T) {
	if os.Getenv("MONIK_SYSTEMD_VERIFY") != "1" {
		t.Skip("explicit systemd unit syntax verification only")
	}
	tool, e := exec.LookPath("systemd-analyze")
	if e != nil {
		t.Skip("systemd-analyze unavailable")
	}
	sock, svc, e := ConsoleUnits("/usr/lib/monik/monik-service-host", "/var/lib/monik-agent", 1000)
	if e != nil {
		t.Fatal(e)
	}
	// verify only: no units installed, no services started. Executable existence
	// is supplied by /bin/true because this is a disposable uninstalled checkout.
	svc = strings.Replace(svc, "/usr/lib/monik/monik-service-host", "/bin/true", 1)
	dir := t.TempDir()
	for n, b := range map[string]string{"monik-console.socket": sock, "monik-console@.service": svc} {
		if e = os.WriteFile(filepath.Join(dir, n), []byte(b), 0600); e != nil {
			t.Fatal(e)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, tool, "--man=no", "verify", filepath.Join(dir, "monik-console.socket"), filepath.Join(dir, "monik-console@.service"))
	raw, e := cmd.CombinedOutput()
	t.Log(string(raw))
	if e != nil {
		t.Fatal(e)
	}
}
