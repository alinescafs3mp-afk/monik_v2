//go:build !linux

package install

import "fmt"

func EnableConsole() error  { return fmt.Errorf("agent console requires Linux/systemd") }
func DisableConsole() error { return fmt.Errorf("agent console requires Linux/systemd") }
