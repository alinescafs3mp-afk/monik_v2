package install

import (
	"fmt"
	"github.com/alinescafs3mp-afk/monik_v2/internal/secure"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

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
	if runtime.GOOS == "windows" {
		return fmt.Errorf("linux installer is not used on windows")
	}
	if opts.Prefix == "" || opts.StateDir == "" || opts.HostSrc == "" || opts.WorkerSrc == "" {
		return fmt.Errorf("prefix, state dir and binary sources are required")
	}
	if opts.User == "" {
		opts.User = "monik"
	}
	for _, c := range opts.User {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return fmt.Errorf("invalid service user")
		}
	}
	if !filepath.IsAbs(opts.Prefix) || !filepath.IsAbs(opts.StateDir) || (opts.ConfigPath != "" && !filepath.IsAbs(opts.ConfigPath)) {
		return fmt.Errorf("service paths must be absolute")
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
	body := servicehost.PlanUnitState(hostDst, opts.ConfigPath, opts.StateDir, opts.User)
	if err := os.MkdirAll(filepath.Dir(opts.UnitPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(opts.UnitPath, []byte(body), 0o644); err != nil {
		return err
	}
	if !opts.SkipSystemctl {
		for _, args := range [][]string{{"daemon-reload"}, {"enable", "--now", "monik-agent.service"}, {"is-active", "--quiet", "monik-agent.service"}} {
			if err := exec.Command("systemctl", args...).Run(); err != nil {
				return fmt.Errorf("systemctl %s failed: %w; service installation is not confirmed", strings.Join(args, " "), err)
			}
		}
	}
	return nil
}
