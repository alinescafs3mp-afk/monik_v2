package install

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func checkSingleLink(info os.FileInfo) error {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("cannot inspect Linux file ownership")
	}
	if info.Mode().IsRegular() && st.Nlink != 1 {
		return fmt.Errorf("hard-linked state file is not permitted")
	}
	return nil
}
func checkTrustedTool(info os.FileInfo) error {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != 0 || info.Mode().Perm()&0022 != 0 {
		return fmt.Errorf("native executable must be root-owned and not writable by others")
	}
	return nil
}

// Ancestors of privileged output paths must not be writable by another account.
// The private state leaf can be owned by the existing service identity.
func checkRootAncestors(path string, allowPrivateLeaf bool) error {
	path = filepath.Clean(path)
	for current := path; current != "/"; current = filepath.Dir(current) {
		info, e := os.Lstat(current)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return e
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("installation path contains a symlink: %s", current)
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return fmt.Errorf("uninspectable install path")
		}
		if current == path && allowPrivateLeaf {
			if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
				return fmt.Errorf("state must be a private directory: %s", current)
			}
			continue
		}
		if st.Uid != 0 || info.Mode().Perm()&0022 != 0 {
			return fmt.Errorf("untrusted installation ancestor: %s; use protected /var/lib/monik-agent state", current)
		}
		if info.IsDir() && info.Mode().Perm()&0001 == 0 {
			return fmt.Errorf("service cannot traverse installation ancestor: %s", current)
		}
	}
	return nil
}

func lockNativeInstall() (func(), error) {
	fd, e := syscall.Open("/run/monik-agent-install.lock", syscall.O_CREAT|syscall.O_RDWR|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return nil, e
	}
	f := os.NewFile(uintptr(fd), "install.lock")
	info, e := f.Stat()
	if e != nil {
		f.Close()
		return nil, e
	}
	if e = checkTrustedToolOwner(info); e != nil {
		f.Close()
		return nil, e
	}
	if e = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		f.Close()
		return nil, fmt.Errorf("another native installation is running: %w", e)
	}
	return func() { _ = syscall.Flock(fd, syscall.LOCK_UN); _ = f.Close() }, nil
}
func checkTrustedToolOwner(info os.FileInfo) error {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || st.Nlink != 1 {
		return fmt.Errorf("unsafe installer lock")
	}
	return nil
}
