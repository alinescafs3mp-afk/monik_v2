//go:build linux

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/installerbundle"
	"github.com/alinescafs3mp-afk/monik_v2/internal/jsonutil"
)

func runNative(ctx context.Context, b *installerbundle.Bundle, out io.Writer) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("run this downloaded file with sudo")
	}
	if b.Manifest.Arch != runtime.GOARCH {
		return fmt.Errorf("installer architecture does not match this machine")
	}
	unlock, e := lockInstall()
	if e != nil {
		return e
	}
	defer unlock()
	var staging string
	defer func() {
		if staging != "" {
			_ = os.RemoveAll(staging)
		}
	}()
	stage := func() error {
		if staging != "" {
			return nil
		}
		root := "/var/lib/monik-installer"
		if e := trustedPath(root, false); e != nil {
			return e
		}
		if e := os.MkdirAll(root, 0700); e != nil {
			return e
		}
		if e := os.Chmod(root, 0700); e != nil {
			return e
		}
		dir, e := os.MkdirTemp(root, "staging-")
		if e != nil {
			return e
		}
		staging = dir
		for _, x := range []struct {
			name string
			copy func(io.Writer) error
		}{{"monik-agent", b.CopyWorker}, {"monik-service-host", b.CopySupervisor}} {
			f, e := os.OpenFile(filepath.Join(staging, x.name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0700)
			if e != nil {
				return e
			}
			e = x.copy(f)
			if e == nil {
				e = f.Sync()
			}
			closeErr := f.Close()
			if e != nil {
				return e
			}
			if closeErr != nil {
				return closeErr
			}
		}
		return nil
	}
	ops := steps{
		now:  time.Now,
		load: loadNativeConfig,
		active: func() bool {
			return systemctl(ctx, "is-active", "--quiet", "monik-agent.service") == nil && systemctl(ctx, "is-enabled", "--quiet", "monik-agent.service") == nil
		},
		preflight: func() error {
			if _, e := os.Stat("/run/systemd/system"); e != nil {
				return fmt.Errorf("running systemd is required; no background-process fallback is used")
			}
			for _, p := range []string{stateDir, "/usr/lib/monik", "/etc/systemd/system/monik-agent.service"} {
				if e := trustedPath(p, p == stateDir); e != nil {
					return e
				}
			}
			if _, e := tool("systemctl"); e != nil {
				return e
			}
			if e := systemctl(ctx, "show", "--property=Version", "--value"); e != nil {
				return fmt.Errorf("systemd manager is not reachable: %w", e)
			}
			return nil
		},
		setup: func(ctx context.Context, b *installerbundle.Bundle) (*installedConfig, error) {
			if e := verifyController(ctx, b.Manifest.Profile); e != nil {
				return nil, e
			}
			if e := stage(); e != nil {
				return nil, e
			}
			p := filepath.Join(staging, "enrollment.json")
			if e := os.WriteFile(p, encodeSetup(b.Manifest.Profile), 0600); e != nil {
				return nil, e
			}
			// Never use a shell, sudo again, PATH-resolved payload or code in argv.
			if e := command(ctx, filepath.Join(staging, "monik-agent"), "setup", "--non-interactive", "--profile", p); e != nil {
				return nil, fmt.Errorf("enrollment failed; saved identity is retained for a safe retry: %w", e)
			}
			c, e := loadNativeConfig()
			if e != nil {
				return nil, e
			}
			if c == nil || c.ControllerID != b.Manifest.Profile.ControllerID {
				return nil, fmt.Errorf("enrollment did not confirm the intended controller")
			}
			return c, nil
		},
		install: func(ctx context.Context, b *installerbundle.Bundle, c *installedConfig) error {
			// An old downloaded installer must not downgrade an existing/newer pair.
			// Partial installs of this exact pair may be retried; differing bytes need
			// explicit recovery or the signed update mechanism, never silent overwrite.
			for _, x := range []struct{ path, digest string }{{"/usr/lib/monik/monik-service-host", b.Manifest.Supervisor.SHA256}, {stateDir + "/bin/monik-agent", b.Manifest.Worker.SHA256}} {
				got, e := fileDigest(x.path)
				if os.IsNotExist(e) {
					continue
				}
				if e != nil {
					return e
				}
				if got != x.digest {
					return fmt.Errorf("an existing different binary was preserved; start/repair that service or use signed updates instead of re-installing an older download")
				}
			}
			if e := stage(); e != nil {
				return e
			}
			if e := command(ctx, filepath.Join(staging, "monik-agent"), "service", "install", "--config", configPath); e != nil {
				return fmt.Errorf("service installation was not confirmed: %w; inspect journalctl -u monik-agent.service; identity remains saved", e)
			}
			return nil
		},
		ready: waitReady,
	}
	return execute(ctx, b, ops, out)
}

func waitReady(ctx context.Context, c *installedConfig) error {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	if e := systemctl(ctx, "is-enabled", "--quiet", "monik-agent.service"); e != nil {
		return fmt.Errorf("service autostart is not confirmed")
	}
	worker, e := fileDigest(stateDir + "/bin/monik-agent")
	if e != nil {
		return e
	}
	host, e := fileDigest("/usr/lib/monik/monik-service-host")
	if e != nil {
		return e
	}
	client := clientFor(c.CACertPEM)
	defer client.CloseIdleConnections()
	firstSession := ""
	var firstSeq int64
	last := "waiting for initial managed report"
	for {
		fresh, e := loadNativeConfig()
		if e == nil && fresh != nil {
			if fresh.ControllerID != c.ControllerID || fresh.ControllerURL != c.ControllerURL {
				return fmt.Errorf("controller binding changed while checking readiness; reopen status without reinstalling")
			}
			cred, e := readPrivateFile(fresh.CredentialPath, 4096)
			if e == nil {
				var r installationStatus
				e = readResponse(ctx, client, strings.TrimRight(c.ControllerURL, "/")+"/api/v1/agent/installation", c.AgentID, strings.TrimSpace(string(cred)), &r)
				if e == nil && r.Ready && r.AgentID == c.AgentID && r.ControllerID == c.ControllerID && r.WorkerDigest == worker && r.SessionID != "" && r.Seq > 0 {
					if firstSession == r.SessionID && r.Seq > firstSeq {
						if e = systemctl(ctx, "is-active", "--quiet", "monik-agent.service"); e == nil {
							if e = supervisorAlive(ctx, worker, host); e == nil {
								return nil
							}
						}
					} else {
						firstSession = r.SessionID
						firstSeq = r.Seq
					}
				} else {
					firstSession = ""
					firstSeq = 0
					if e == nil {
						last = "controller has not confirmed this managed process and configuration"
					}
				}
				if e != nil {
					last = e.Error()
				}
			} else {
				last = "credential could not be read"
			}
		} else {
			last = "managed identity is not readable"
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("service may be installed, but readiness is NOT confirmed: %s; do not re-enroll; check journalctl -u monik-agent.service and rerun this same file", last)
		case <-timer.C:
		}
	}
}
func supervisorAlive(ctx context.Context, workerDigest, hostDigest string) error {
	d := net.Dialer{Timeout: time.Second}
	conn, e := d.DialContext(ctx, "unix", stateDir+"/service-host.sock")
	if e != nil {
		return fmt.Errorf("supervisor control socket is unavailable")
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, e = io.WriteString(conn, "{\"action\":\"status\"}\n"); e != nil {
		return e
	}
	// Existing supervisor closes a connection after this single bounded response.
	var r struct {
		OK      bool   `json:"ok"`
		Message string `json:"message"`
		PID     int    `json:"pid"`
	}
	if e = jsonutil.ReadObject(conn, 4096, &r); e != nil {
		return e
	}
	if !r.OK || r.Message != "running" || r.PID <= 0 {
		return fmt.Errorf("supervisor has no live worker")
	}
	// Verify the kernel's executable handles, not just filenames or a saved flag.
	if got, e := processDigest(r.PID); e != nil || got != workerDigest {
		return fmt.Errorf("running worker executable does not match the installed slot")
	}
	toolPath, e := tool("systemctl")
	if e != nil {
		return e
	}
	checkCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(checkCtx, toolPath, "show", "monik-agent.service", "--property=MainPID", "--value")
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "HOME=/root", "LANG=C", "LC_ALL=C"}
	raw, e := cmd.Output()
	if e != nil || len(raw) > 64 {
		return fmt.Errorf("systemd did not confirm supervisor PID")
	}
	pid, e := strconv.Atoi(strings.TrimSpace(string(raw)))
	if e != nil || pid <= 0 {
		return fmt.Errorf("invalid supervisor PID")
	}
	if got, e := processDigest(pid); e != nil || got != hostDigest {
		return fmt.Errorf("running supervisor executable does not match the installed file")
	}
	return nil
}
func loadNativeConfig() (*installedConfig, error) {
	b, e := readPrivateFile(configPath, 128<<10)
	if os.IsNotExist(e) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	var c installedConfig
	if e = jsonutil.Unmarshal(b, &c); e != nil {
		return nil, e
	}
	if c.AgentID == "" || len(c.AgentID) > 128 || strings.ContainsAny(c.AgentID, "/\\\r\n :") || c.StateDir != stateDir || c.ControllerURL == "" || c.CACertPEM == "" {
		return nil, fmt.Errorf("invalid existing agent identity; automatic replacement is refused")
	}
	if !strings.HasPrefix(c.CredentialPath, stateDir+"/") || filepath.Clean(c.CredentialPath) != c.CredentialPath {
		return nil, fmt.Errorf("credential must remain inside the protected agent state")
	}
	if _, e := readPrivateFile(c.CredentialPath, 4096); e != nil {
		return nil, e
	}
	return &c, nil
}

// Open each path component relative to the previously opened directory. Unlike
// lstat-then-open, this cannot follow a substituted parent symlink as root.
func openNoLinks(p string) (*os.File, error) {
	if !filepath.IsAbs(p) || filepath.Clean(p) != p || p == "/" {
		return nil, fmt.Errorf("invalid protected path")
	}
	fd, e := syscall.Open("/", syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC, 0)
	if e != nil {
		return nil, e
	}
	parts := strings.Split(strings.TrimPrefix(p, "/"), "/")
	for i, name := range parts {
		flags := syscall.O_RDONLY | syscall.O_CLOEXEC | syscall.O_NOFOLLOW | syscall.O_NONBLOCK
		if i < len(parts)-1 {
			flags |= syscall.O_DIRECTORY
		}
		next, err := syscall.Openat(fd, name, flags, 0)
		_ = syscall.Close(fd)
		if err != nil {
			return nil, &os.PathError{Op: "open", Path: p, Err: err}
		}
		fd = next
	}
	return os.NewFile(uintptr(fd), p), nil
}
func readPrivateFile(p string, max int64) ([]byte, error) {
	f, e := openNoLinks(p)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	i, e := f.Stat()
	if e != nil {
		return nil, e
	}
	if !i.Mode().IsRegular() || i.Size() > max || i.Mode().Perm()&0077 != 0 {
		return nil, fmt.Errorf("invalid private state file")
	}
	st := i.Sys().(*syscall.Stat_t)
	if st.Nlink != 1 {
		return nil, fmt.Errorf("hard-linked private state refused")
	}
	b, e := io.ReadAll(io.LimitReader(f, max+1))
	if int64(len(b)) > max {
		return nil, fmt.Errorf("private file exceeds limit")
	}
	return b, e
}
func fileDigest(p string) (string, error) {
	f, e := openNoLinks(p)
	if e != nil {
		return "", e
	}
	defer f.Close()
	i, e := f.Stat()
	if e != nil {
		return "", e
	}
	if !i.Mode().IsRegular() || i.Size() > installerbundle.MaxExecutable {
		return "", fmt.Errorf("invalid installed executable")
	}
	h := sha256.New()
	n, e := io.Copy(h, io.LimitReader(f, installerbundle.MaxExecutable+1))
	if e != nil {
		return "", e
	}
	if n > installerbundle.MaxExecutable {
		return "", fmt.Errorf("executable exceeds limit")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func tool(name string) (string, error) {
	for _, dir := range []string{"/usr/bin", "/usr/sbin", "/bin", "/sbin"} {
		p := filepath.Join(dir, name)
		i, e := os.Stat(p)
		if e == nil && i.Mode().IsRegular() && i.Mode()&0111 != 0 {
			st := i.Sys().(*syscall.Stat_t)
			if st.Uid != 0 || i.Mode().Perm()&0022 != 0 {
				return "", fmt.Errorf("untrusted system executable")
			}
			return p, nil
		}
	}
	return "", fmt.Errorf("required system tool %s is missing", name)
}
func systemctl(ctx context.Context, args ...string) error {
	p, e := tool("systemctl")
	if e != nil {
		return e
	}
	return command(ctx, p, args...)
}
func command(ctx context.Context, path string, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "HOME=/root", "LANG=C", "LC_ALL=C"}
	// Do not echo subprocess output: profiles and runtime errors may contain
	// enrollment material. systemd persists service diagnostics independently.
	if e := cmd.Run(); e != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("%s timed out or was cancelled", filepath.Base(path))
		}
		return fmt.Errorf("%s failed (%v)", filepath.Base(path), e)
	}
	return nil
}
func trustedPath(p string, allowStateLeaf bool) error {
	for cur := p; cur != "/"; cur = filepath.Dir(cur) {
		i, e := os.Lstat(cur)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return e
		}
		st := i.Sys().(*syscall.Stat_t)
		if i.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("linked installation path refused: %s", cur)
		}
		if cur == p && allowStateLeaf {
			if !i.IsDir() || i.Mode().Perm()&0077 != 0 {
				return fmt.Errorf("agent state is not private")
			}
			continue
		}
		if st.Uid != 0 || i.Mode().Perm()&0022 != 0 {
			return fmt.Errorf("untrusted installation path: %s", cur)
		}
	}
	return nil
}
func lockInstall() (func(), error) {
	fd, e := syscall.Open("/run/monik-single-install.lock", syscall.O_RDWR|syscall.O_CREAT|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0600)
	if e != nil {
		return nil, e
	}
	f := os.NewFile(uintptr(fd), "installer lock")
	i, e := f.Stat()
	if e != nil {
		f.Close()
		return nil, e
	}
	st := i.Sys().(*syscall.Stat_t)
	if !i.Mode().IsRegular() || st.Uid != 0 || st.Nlink != 1 || i.Mode().Perm()&0077 != 0 {
		f.Close()
		return nil, fmt.Errorf("unsafe installer lock")
	}
	if e = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		f.Close()
		return nil, fmt.Errorf("another single-file installation is in progress")
	}
	return func() { _ = syscall.Flock(fd, syscall.LOCK_UN); _ = f.Close() }, nil
}

func processDigest(pid int) (string, error) {
	f, e := os.Open(fmt.Sprintf("/proc/%d/exe", pid))
	if e != nil {
		return "", e
	}
	defer f.Close()
	i, e := f.Stat()
	if e != nil {
		return "", e
	}
	if !i.Mode().IsRegular() || i.Size() > installerbundle.MaxExecutable {
		return "", fmt.Errorf("invalid process executable")
	}
	h := sha256.New()
	n, e := io.Copy(h, io.LimitReader(f, installerbundle.MaxExecutable+1))
	if e != nil {
		return "", e
	}
	if n > installerbundle.MaxExecutable {
		return "", fmt.Errorf("process executable exceeds limit")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
