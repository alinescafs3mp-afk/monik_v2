//go:build !windows

package install

import "fmt"

func WindowsDefaults() Options {
	return Options{Prefix: `C:\Program Files\Monik`, StateDir: `C:\ProgramData\Monik\agent`}
}

func InstallWindows(opts Options) error {
	return fmt.Errorf("windows service installation is only available on windows")
}

func UninstallWindows() error {
	return fmt.Errorf("windows service removal is only available on windows")
}
