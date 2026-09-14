package install

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/agent/configfile"
	"github.com/alinescafs3mp-afk/monik_v2/internal/idgen"
	"github.com/alinescafs3mp-afk/monik_v2/internal/secure"
	"github.com/alinescafs3mp-afk/monik_v2/internal/servicehost"
)

type Options struct {
	Prefix        string
	StateDir      string
	ConfigPath    string
	UnitPath      string
	User          string
	HostSrc       string
	WorkerSrc     string
	SkipSystemctl bool
}

func LinuxDefaults() Options {
	return Options{
		Prefix:   "/usr/lib/monik",
		StateDir: "/var/lib/monik-agent",
		UnitPath: "/etc/systemd/system/monik-agent.service",
		User:     "monik",
	}
}

func copyFile(src, dst string, mode os.FileMode) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("source must be a regular file")
	}
	if target, e := os.Stat(dst); e == nil && os.SameFile(info, target) {
		return nil
	}
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return secure.AtomicWrite(dst, b, mode)
}

func InstallLinux(opts Options) error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("linux installer is not used on windows")
	}
	if opts.Prefix == "" || opts.StateDir == "" || opts.HostSrc == "" || opts.WorkerSrc == "" {
		return fmt.Errorf("prefix, state dir and binary sources are required")
	}
	if opts.User == "" {
		opts.User = "monik"
	}
	if len(opts.User) > 32 || !(opts.User[0] >= 'a' && opts.User[0] <= 'z' || opts.User[0] == '_') {
		return fmt.Errorf("invalid dedicated service user")
	}
	for _, c := range opts.User {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return fmt.Errorf("invalid service user")
		}
	}
	if !filepath.IsAbs(opts.Prefix) || !filepath.IsAbs(opts.StateDir) || (opts.ConfigPath != "" && !filepath.IsAbs(opts.ConfigPath)) {
		return fmt.Errorf("service paths must be absolute")
	}
	if opts.UnitPath != "" && !filepath.IsAbs(opts.UnitPath) {
		return fmt.Errorf("unit path must be absolute")
	}
	if unsafeInstallDir(opts.Prefix) || unsafeInstallDir(opts.StateDir) || withinDir(opts.Prefix, opts.StateDir) || withinDir(opts.StateDir, opts.Prefix) {
		return fmt.Errorf("unsafe installation directory")
	}
	if !opts.SkipSystemctl && os.Geteuid() != 0 {
		return fmt.Errorf("managed installation requires root")
	}
	if !opts.SkipSystemctl {
		unlock, err := lockNativeInstall()
		if err != nil {
			return err
		}
		defer unlock()
	}
	var systemctl string
	var state *configfile.State
	var uid, gid int
	if !opts.SkipSystemctl {
		if opts.ConfigPath == "" {
			opts.ConfigPath = filepath.Join(opts.StateDir, "agent.json")
		}
		var err error
		if systemctl, err = systemTool("systemctl"); err != nil {
			return err
		}
		if err = checkRootAncestors(opts.Prefix, false); err != nil {
			return err
		}
		if err = checkRootAncestors(opts.StateDir, true); err != nil {
			return err
		}
		if err = checkRootAncestors(opts.UnitPath, false); err != nil {
			return err
		}
		if filepath.Base(opts.UnitPath) != "monik-agent.service" {
			return fmt.Errorf("unexpected service unit name")
		}
		if err = inspectPrivateState(opts.StateDir); err != nil {
			return err
		}
		state, err = configfile.Load(opts.ConfigPath)
		if err != nil {
			return fmt.Errorf("load existing enrollment before installing: %w", err)
		}
		if filepath.Clean(state.File.StateDir) != filepath.Clean(opts.StateDir) || filepath.Dir(filepath.Clean(opts.ConfigPath)) != filepath.Clean(opts.StateDir) || !withinDir(opts.StateDir, state.File.CredentialPath) {
			return fmt.Errorf("config, credential and state must share the selected state directory; no identity is moved implicitly")
		}
		u, e := ensureServiceUser(opts.User)
		if e != nil {
			return e
		}
		uid, e = strconv.Atoi(u.Uid)
		if e != nil {
			return e
		}
		gid, e = strconv.Atoi(u.Gid)
		if e != nil {
			return e
		}
		if uid == 0 || gid == 0 {
			return fmt.Errorf("collector service cannot run as root")
		}
		// Explicit installation may restart this one service, never the host.
		if runSystemTool(systemctl, "is-active", "--quiet", "monik-agent.service") == nil {
			if err = runSystemTool(systemctl, "stop", "monik-agent.service"); err != nil {
				return fmt.Errorf("stop existing agent before install: %w", err)
			}
		}
		// A live agent can commit a newer URL/credential generation during preflight.
		// Reload after it has stopped instead of publishing stale enrollment fields.
		latest, e := readPrivateConfig(opts.StateDir, filepath.Base(opts.ConfigPath))
		if e != nil {
			return e
		}
		if latest.AgentID != state.File.AgentID || filepath.Clean(latest.StateDir) != filepath.Clean(opts.StateDir) || !withinDir(opts.StateDir, latest.CredentialPath) {
			return fmt.Errorf("enrollment changed incompatibly during installation")
		}
		state.File = latest
	}
	if err := os.MkdirAll(opts.Prefix, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(opts.StateDir, 0o700); err != nil {
		return err
	}
	hostDst := filepath.Join(opts.Prefix, "monik-service-host")
	workerDst := filepath.Join(opts.Prefix, "monik-agent")
	if err := copyFile(opts.HostSrc, hostDst, 0o755); err != nil {
		return err
	}
	if err := copyFile(opts.WorkerSrc, workerDst, 0o755); err != nil {
		return err
	}
	if opts.ConfigPath == "" {
		opts.ConfigPath = filepath.Join(opts.StateDir, "agent.json")
	}
	if opts.UnitPath == "" {
		opts.UnitPath = "/etc/systemd/system/monik-agent.service"
	}
	// The immutable supervisor stays in the root-owned prefix. Its worker slot
	// lives in private service state so signed worker replacement needs no root.
	activeWorker := filepath.Join(opts.StateDir, "bin", "monik-agent")
	workerBytes, err := os.ReadFile(workerDst)
	if err != nil {
		return err
	}
	if err := writePrivateState(opts.StateDir, "bin/monik-agent", workerBytes, 0755); err != nil {
		return err
	}
	if state != nil {
		state.File.Managed = true
		configBytes, err := json.MarshalIndent(state.File, "", "  ")
		if err != nil {
			return err
		}
		if err := writePrivateState(opts.StateDir, filepath.Base(opts.ConfigPath), configBytes, 0600); err != nil {
			return fmt.Errorf("persist managed identity: %w", err)
		}
		sock := filepath.Join(opts.StateDir, "service-host.sock")
		if info, e := os.Lstat(sock); e == nil && info.Mode()&os.ModeSocket != 0 {
			if e = os.Remove(sock); e != nil {
				return e
			}
		}
		if err := ownPrivateState(opts.StateDir, uid, gid); err != nil {
			return err
		}
	}
	body := servicehost.PlanManagedUnit(hostDst, activeWorker, opts.ConfigPath, opts.StateDir, opts.User)
	if err := os.MkdirAll(filepath.Dir(opts.UnitPath), 0o755); err != nil {
		return err
	}
	if err := secure.AtomicWrite(opts.UnitPath, []byte(body), 0o644); err != nil {
		return err
	}
	if !opts.SkipSystemctl {
		for _, args := range [][]string{{"daemon-reload"}, {"enable", "monik-agent.service"}, {"restart", "monik-agent.service"}, {"is-active", "--quiet", "monik-agent.service"}} {
			if err := runSystemTool(systemctl, args...); err != nil {
				return fmt.Errorf("systemctl %s failed: %w; service installation is not confirmed", strings.Join(args, " "), err)
			}
		}
	}
	return nil
}

func withinDir(root, path string) bool {
	if !filepath.IsAbs(root) || !filepath.IsAbs(path) {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
func ensureServiceUser(name string) (*user.User, error) {
	u, err := user.Lookup(name)
	if err == nil {
		return u, nil
	}
	if _, ok := err.(user.UnknownUserError); !ok {
		return nil, err
	}
	shell := "/usr/sbin/nologin"
	if _, e := os.Stat(shell); e != nil {
		shell = "/sbin/nologin"
	}
	if _, e := os.Stat(shell); e != nil {
		return nil, fmt.Errorf("nologin shell unavailable")
	}
	tool, e := systemTool("useradd")
	if e != nil {
		return nil, e
	}
	if e := runSystemTool(tool, "--system", "--user-group", "--home-dir", "/nonexistent", "--no-create-home", "--shell", shell, name); e != nil {
		return nil, fmt.Errorf("create dedicated service account: %w", e)
	}
	return user.Lookup(name)
}

// Reject linked/nonregular state. Only an explicit elevated installation calls
// this; the monitoring API cannot change identities or filesystem ownership.
func inspectPrivateState(root string) error {
	r, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer r.Close()
	return fs.WalkDir(r.FS(), ".", func(name string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		info, e := r.Lstat(name)
		if e != nil {
			return e
		}
		if name == "service-host.sock" && info.Mode()&os.ModeSocket != 0 {
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
			return fmt.Errorf("state must contain only regular private files/directories: %s", name)
		}
		return checkSingleLink(info)
	})
}
func ownPrivateState(root string, uid, gid int) error {
	r, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer r.Close()
	return fs.WalkDir(r.FS(), ".", func(name string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		info, e := r.Lstat(name)
		if e != nil {
			return e
		}
		if info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
			return fmt.Errorf("nonregular state entry: %s", name)
		}
		f, e := r.Open(name)
		if e != nil {
			return e
		}
		defer f.Close()
		actual, e := f.Stat()
		if e != nil {
			return e
		}
		if !os.SameFile(info, actual) {
			return fmt.Errorf("state changed during installation")
		}
		if e = checkSingleLink(actual); e != nil {
			return e
		}
		mode := os.FileMode(0600)
		if info.IsDir() || info.Mode()&0111 != 0 {
			mode = 0700
		}
		if e = f.Chmod(mode); e != nil {
			return e
		}
		return f.Chown(uid, gid)
	})
}
func unsafeInstallDir(p string) bool {
	switch filepath.Clean(p) {
	case "/", "/etc", "/usr", "/usr/lib", "/usr/bin", "/var", "/var/lib", "/home", "/root", "/tmp", "/run", "/opt":
		return true
	}
	return false
}

// Elevated installation never resolves privileged tools through caller PATH.
func systemTool(name string) (string, error) {
	for _, dir := range []string{"/usr/sbin", "/usr/bin", "/sbin", "/bin"} {
		p := filepath.Join(dir, name)
		i, e := os.Stat(p)
		if e == nil && i.Mode().IsRegular() && i.Mode()&0111 != 0 {
			if e = checkTrustedTool(i); e != nil {
				return "", e
			}
			return p, nil
		}
	}
	return "", fmt.Errorf("required native tool %s unavailable", name)
}
func runSystemTool(path string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C"}
	return cmd.Run()
}

func UninstallLinux() error {
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		return fmt.Errorf("Linux root authority required")
	}
	unlock, e := lockNativeInstall()
	if e != nil {
		return e
	}
	defer unlock()
	tool, e := systemTool("systemctl")
	if e != nil {
		return e
	}
	if err := runSystemTool(tool, "disable", "--now", "monik-agent.service"); err != nil {
		return err
	}
	if err := os.Remove("/etc/systemd/system/monik-agent.service"); err != nil && !os.IsNotExist(err) {
		return err
	}
	return runSystemTool(tool, "daemon-reload")
}

// Descriptor-relative publication keeps privileged writes inside the selected
// private state even if an existing service-owned subdirectory is replaced.
func writePrivateState(dir, name string, b []byte, mode os.FileMode) error {
	r, e := os.OpenRoot(dir)
	if e != nil {
		return e
	}
	defer r.Close()
	parent := filepath.Dir(name)
	if e = r.MkdirAll(parent, 0700); e != nil {
		return e
	}
	nonce, e := idgen.Secret(12)
	if e != nil {
		return e
	}
	tmp := filepath.Join(parent, ".monik-install-"+nonce)
	f, e := r.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if e != nil {
		return e
	}
	defer f.Close()
	defer r.Remove(tmp)
	if _, e = f.Write(b); e != nil {
		return e
	}
	if e = f.Sync(); e != nil {
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	if e = r.Rename(tmp, name); e != nil {
		return e
	}
	d, e := r.Open(parent)
	if e != nil {
		return e
	}
	defer d.Close()
	return d.Sync()
}

func readPrivateConfig(dir, name string) (configfile.File, error) {
	var f configfile.File
	r, e := os.OpenRoot(dir)
	if e != nil {
		return f, e
	}
	defer r.Close()
	b, e := r.ReadFile(name)
	if e != nil {
		return f, e
	}
	e = json.Unmarshal(b, &f)
	return f, e
}
