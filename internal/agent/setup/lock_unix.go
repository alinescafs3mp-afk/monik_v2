//go:build !windows

package setup

import (
	"fmt"
	"golang.org/x/sys/unix"
	"os"
)

// The lock is released by the OS on crash. Keep its inode to avoid split locks.
func acquireSetupLock(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("another setup is using this state directory: %w", err)
	}
	return func() { _ = unix.Flock(int(f.Fd()), unix.LOCK_UN); _ = f.Close() }, nil
}
