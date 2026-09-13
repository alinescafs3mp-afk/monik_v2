package install

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

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
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Chmod(mode)
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
		_ = exec.Command("systemctl", "daemon-reload").Run()
		_ = exec.Command("systemctl", "enable", "--now", "monik-agent.service").Run()
	}
	return nil
}
