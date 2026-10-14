//go:build linux

package spark

import (
	"bytes"
	"context"
	"fmt"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// trustedPath rejects symlinks and writable substitution in every ancestor.
// A privileged broker never interprets an owner-writable home launcher as root.
func trustedPath(path string, owner uint32, directory bool) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return fmt.Errorf("absolute clean trusted path required")
	}
	for p := path; ; p = filepath.Dir(p) {
		st, err := os.Lstat(p)
		if err != nil {
			return fmt.Errorf("trusted path unavailable")
		}
		sys, ok := st.Sys().(*syscall.Stat_t)
		if !ok || st.Mode()&os.ModeSymlink != 0 || (sys.Uid != owner && (p == path || sys.Uid != 0)) || st.Mode().Perm()&0022 != 0 {
			return fmt.Errorf("trusted path owner/mode mismatch")
		}
		if p == path {
			if directory && !st.IsDir() || !directory && !st.Mode().IsRegular() {
				return fmt.Errorf("trusted path type mismatch")
			}
		}
		if p == "/" {
			break
		}
	}
	return nil
}
func acquireSharedLock(path string) (func(), error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, fmt.Errorf("invalid shared lock path")
	}
	fd, e := unix.Open(path, unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if e != nil {
		return nil, e
	}
	file := os.NewFile(uintptr(fd), path)
	st, e := file.Stat()
	if e != nil {
		file.Close()
		return nil, e
	}
	native, ok := st.Sys().(*syscall.Stat_t)
	if !ok || native.Uid != 0 || !st.Mode().IsRegular() || st.Mode().Perm() != 0600 {
		file.Close()
		return nil, fmt.Errorf("shared lock must be existing root private regular inode")
	}
	for parent := filepath.Dir(path); ; parent = filepath.Dir(parent) {
		info, err := os.Lstat(parent)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			file.Close()
			return nil, fmt.Errorf("shared lock ancestor unsafe")
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != 0 || info.Mode().Perm()&0022 != 0 && info.Mode()&os.ModeSticky == 0 {
			file.Close()
			return nil, fmt.Errorf("shared lock ancestor writable")
		}
		if parent == "/" {
			break
		}
	}
	if e = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); e != nil {
		file.Close()
		return nil, e
	}
	var once sync.Once
	return func() { once.Do(func() { _ = unix.Flock(fd, unix.LOCK_UN); file.Close() }) }, nil
}

type boundedWriter struct {
	buffer   bytes.Buffer
	limit    int
	exceeded bool
}

func (b *boundedWriter) Len() int      { return b.buffer.Len() }
func (b *boundedWriter) Bytes() []byte { return b.buffer.Bytes() }

func (b *boundedWriter) Write(p []byte) (int, error) {
	n := len(p)
	if b.Len()+n > b.limit {
		b.exceeded = true
		keep := b.limit - b.Len()
		if keep > 0 {
			b.buffer.Write(p[:keep])
		}
		return n, nil
	}
	b.buffer.Write(p)
	return n, nil
}

// Fixed executable/argv come exclusively from local adapters. Bounded capture
// never reports raw stderr, which can contain environment/argument secrets.
func fixedCommand(ctx context.Context, path string, args []string, uid uint32, limit int) ([]byte, error) {
	if err := trustedPath(path, 0, false); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LANG=C", "LC_ALL=C"}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if uid != 0 {
		cmd.Env = append(cmd.Env, "XDG_RUNTIME_DIR=/run/user/"+strconv.FormatUint(uint64(uid), 10), "DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/"+strconv.FormatUint(uint64(uid), 10)+"/bus")
		if os.Geteuid() == 0 {
			cmd.SysProcAttr.Credential = &syscall.Credential{Uid: uid, Gid: uid, Groups: []uint32{}}
		} else if uint32(os.Geteuid()) != uid {
			return nil, fmt.Errorf("user-manager identity unavailable")
		}
	}
	out := &boundedWriter{limit: limit}
	cmd.Stdout = out
	cmd.Stderr = io.Discard
	cmd.WaitDelay = 200 * time.Millisecond
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("approved supervisor call failed")
	}
	if out.exceeded {
		return nil, fmt.Errorf("supervisor output limit exceeded")
	}
	return out.Bytes(), nil
}
func collectorIdentityAllowed(uid uint32, pid int) error {
	if uid == 0 {
		return fmt.Errorf("collector must run non-root")
	}
	b, e := readBoundedFile("/etc/group", 1<<20)
	if e != nil {
		return fmt.Errorf("cannot verify collector groups")
	}
	dockerGID := ""
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Split(line, ":")
		if len(f) >= 3 && f[0] == "docker" {
			dockerGID = f[2]
		}
	}
	if dockerGID == "" {
		return nil
	}
	status, e := readBoundedFile(fmt.Sprintf("/proc/%d/status", pid), 64<<10)
	if e != nil {
		return fmt.Errorf("cannot verify collector process groups")
	}
	for _, line := range strings.Split(string(status), "\n") {
		if strings.HasPrefix(line, "Groups:") || strings.HasPrefix(line, "Gid:") {
			for _, g := range strings.Fields(strings.TrimPrefix(strings.TrimPrefix(line, "Groups:"), "Gid:")) {
				if g == dockerGID {
					return fmt.Errorf("collector must be outside Docker group")
				}
			}
		}
	}
	return nil
}
func readPrivateFile(path string, max int64, owner uint32) ([]byte, error) {
	fd, e := unix.Open(path, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if e != nil {
		return nil, e
	}
	file := os.NewFile(uintptr(fd), path)
	defer file.Close()
	info, e := file.Stat()
	if e != nil {
		return nil, e
	}
	native, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || native.Uid != owner || native.Nlink != 1 || info.Size() > max {
		return nil, fmt.Errorf("protected journal owner/type/size mismatch")
	}
	b, e := io.ReadAll(io.LimitReader(file, max+1))
	if e != nil || int64(len(b)) > max {
		return nil, fmt.Errorf("protected journal read budget")
	}
	return b, nil
}
