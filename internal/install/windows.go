//go:build windows

package install

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

func WindowsDefaults() Options {
	root := filepath.Join(os.Getenv("ProgramFiles"), "Monik")
	state := filepath.Join(os.Getenv("ProgramData"), "Monik", "agent")
	return Options{Prefix: root, StateDir: state, User: "NT AUTHORITY\\LocalService"}
}

func InstallWindows(opts Options) error {
	if opts.Prefix == "" || opts.HostSrc == "" || opts.WorkerSrc == "" {
		return fmt.Errorf("prefix and binary sources are required")
	}
	if err := os.MkdirAll(opts.Prefix, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(opts.StateDir, 0o700); err != nil {
		return err
	}
	hostDst := filepath.Join(opts.Prefix, "monik-service-host.exe")
	workerDst := filepath.Join(opts.Prefix, "monik-agent.exe")
	if err := copyFile(opts.HostSrc, hostDst, 0o755); err != nil {
		return err
	}
	if err := copyFile(opts.WorkerSrc, workerDst, 0o755); err != nil {
		return err
	}
	if opts.ConfigPath == "" {
		opts.ConfigPath = filepath.Join(opts.StateDir, "agent.json")
	}
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	args := []string{"run", "--worker", workerDst, "--config", opts.ConfigPath, "--state", opts.StateDir}
	bin := windows.ComposeCommandLine(append([]string{hostDst}, args...))
	cfg := mgr.Config{
		DisplayName:      "Monik agent service host",
		Description:      "Supervises the Monik collector worker and signed updates",
		StartType:        mgr.StartAutomatic,
		ServiceStartName: "",
	}
	s, err := m.OpenService("MonikAgent")
	if err != nil {
		s, err = m.CreateService("MonikAgent", hostDst, cfg, args...)
		if err != nil {
			return err
		}
	}
	defer s.Close()
	actual, err := s.Config()
	if err != nil {
		return err
	}
	actual.BinaryPathName = bin
	actual.StartType = mgr.StartAutomatic
	if err := s.UpdateConfig(actual); err != nil {
		return err
	}
	if err := s.SetRecoveryActions([]mgr.RecoveryAction{{Type: mgr.ServiceRestart, Delay: 5 * time.Second}}, 60); err != nil {
		return err
	}
	status, err := s.Query()
	if err != nil {
		return err
	}
	if status.State == svc.Running {
		return nil
	}
	return s.Start()
}

func UninstallWindows() error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService("MonikAgent")
	if err != nil {
		return err
	}
	defer s.Close()
	_, _ = s.Control(svc.Stop)
	return s.Delete()
}
