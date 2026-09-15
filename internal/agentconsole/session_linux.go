//go:build linux

package agentconsole

import (
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/user"
	"runtime"
	"strconv"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// ServeActivated runs in a systemd per-connection unit under a DIFFERENT,
// non-root UID. PID 1 owns the listening socket and enforces filesystem access;
// this process has no network listener, setuid helper or credential reader.
func ServeActivated(ctx context.Context, expectedAgentUID int) error {
	if expectedAgentUID <= 0 || os.Geteuid() == 0 || os.Geteuid() == expectedAgentUID {
		return fmt.Errorf("console requires a distinct non-root account")
	}
	// no_new_privs is per-thread; pin through shell creation so the Go
	// scheduler cannot move exec to a thread missing this additional guard.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	var capabilities [2]unix.CapUserData
	if e := unix.Capget(&unix.CapUserHeader{Version: unix.LINUX_CAPABILITY_VERSION_3}, &capabilities[0]); e != nil {
		return e
	}
	for _, part := range capabilities {
		if part.Effective != 0 || part.Permitted != 0 || part.Inheritable != 0 {
			return fmt.Errorf("console capability sets must be empty")
		}
	}
	// Enforce independently of unit options. This also forbids sudo/setuid gains.
	if e := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); e != nil {
		return e
	}
	f := os.NewFile(0, "systemd-accepted-console")
	if f == nil {
		return fmt.Errorf("accepted socket missing")
	}
	c, e := net.FileConn(f)
	_ = f.Close() // FileConn duplicates the accepted descriptor.
	if e != nil {
		return fmt.Errorf("accepted Unix socket required")
	}
	defer c.Close()
	u, ok := c.(*net.UnixConn)
	if !ok {
		return fmt.Errorf("local Unix transport required")
	}
	raw, e := u.SyscallConn()
	if e != nil {
		return e
	}
	var cred *unix.Ucred
	var pe error
	if e = raw.Control(func(fd uintptr) { cred, pe = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED) }); e != nil || pe != nil || cred == nil || int(cred.Uid) != expectedAgentUID {
		return fmt.Errorf("local peer denied")
	}
	account, e := user.LookupId(strconv.Itoa(os.Geteuid()))
	if e != nil {
		return e
	}
	return ServeLocal(ctx, c, account.Username)
}
func openPTY(cols, rows int) (*os.File, *os.File, error) {
	fd, e := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if e != nil {
		return nil, nil, e
	}
	master := os.NewFile(uintptr(fd), "pty-master")
	ok := false
	defer func() {
		if !ok {
			master.Close()
		}
	}()
	if e = unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, 0); e != nil {
		return nil, nil, e
	}
	n, e := unix.IoctlGetInt(fd, unix.TIOCGPTN)
	if e != nil {
		return nil, nil, e
	}
	slave, e := os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|syscall.O_NOCTTY, 0)
	if e != nil {
		return nil, nil, e
	}
	if e = unix.IoctlSetWinsize(fd, unix.TIOCSWINSZ, &unix.Winsize{Col: uint16(cols), Row: uint16(rows)}); e != nil {
		slave.Close()
		return nil, nil, e
	}
	ok = true
	return master, slave, nil
}

// ServeLocal is also exercised using real PTYs in tests. Production always
// enters via ServeActivated, which checks OS credentials before any request.
func ServeLocal(ctx context.Context, c net.Conn, username string) error {
	local := NewLocal(c)
	defer local.Close()
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	hello, e := local.Read()
	if e != nil {
		return e
	}
	if hello.Type == "info" && hello.Session == "" && hello.Ticket == "" && hello.Data == "" && hello.User == "" && hello.Message == "" && hello.Seq == 0 && hello.Cols == 0 && hello.Rows == 0 {
		return local.Send(Message{Type: "info", User: username})
	}
	if hello.Type != "open" || !ID(hello.Session) || !Size(hello.Cols, hello.Rows) || hello.Ticket != "" || hello.User != "" || hello.Message != "" || hello.Data != "" || hello.Seq != 0 {
		return fmt.Errorf("invalid local console start")
	}
	_ = c.SetReadDeadline(time.Time{})
	master, slave, e := openPTY(hello.Cols, hello.Rows)
	if e != nil {
		return fmt.Errorf("PTY unavailable")
	}
	defer master.Close()
	defer slave.Close()
	// No command, path, environment or username comes from the network.
	cmd := exec.Command("/bin/sh", "-i")
	home := os.Getenv("HOME")
	if home == "" {
		home = "/"
	}
	cmd.Dir = home
	cmd.Env = []string{"PATH=/usr/local/bin:/usr/bin:/bin", "TERM=xterm-256color", "LANG=C.UTF-8", "HOME=" + home, "USER=" + username, "LOGNAME=" + username}
	cmd.Stdin = slave
	cmd.Stdout = slave
	cmd.Stderr = slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0, Pdeathsig: syscall.SIGHUP}
	if e = cmd.Start(); e != nil {
		return fmt.Errorf("shell could not start")
	}
	slave.Close()
	runctx, cancel := context.WithTimeout(ctx, Lifetime)
	defer cancel()
	var wg sync.WaitGroup
	exited := make(chan struct{})
	wg.Add(1)
	go func() { defer wg.Done(); _ = cmd.Wait(); close(exited); cancel() }()
	defer func() {
		cancel()
		_ = local.Close()
		_ = master.Close()
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		wg.Wait()
	}()
	if e = local.Send(Message{Type: "ready", Session: hello.Session, User: username}); e != nil {
		return e
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer cancel()
		var total int
		buf := make([]byte, MaxOutput)
		for {
			n, e := master.Read(buf)
			if n > 0 {
				total += n
				if total > OutputBudget {
					return
				}
				if local.Send(Message{Type: "output", Session: hello.Session, Data: base64.StdEncoding.EncodeToString(buf[:n])}) != nil {
					return
				}
			}
			if e != nil {
				return
			}
		}
	}()
	// Closing the connection unblocks all reads/writes on disconnect or lifetime.
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-runctx.Done()
		_ = local.Close()
		_ = master.Close()
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}()
	last := int64(0)
	lastInput := time.Now()
	for {
		_ = c.SetReadDeadline(lastInput.Add(Idle))
		m, e := local.Read()
		if e != nil {
			return nil
		}
		if m.Session != hello.Session || !ValidateInput(m, last) {
			return fmt.Errorf("invalid local console input")
		}
		last = m.Seq
		switch m.Type {
		case "close":
			return nil
		case "input":
			lastInput = time.Now()
			if _, e = master.Write([]byte(m.Data)); e != nil {
				return nil
			}
		case "resize":
			if e = unix.IoctlSetWinsize(int(master.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Col: uint16(m.Cols), Row: uint16(m.Rows)}); e != nil {
				return e
			}
		}
	}
}

// Local consent is the root-managed systemd socket, not a remotely writable
// AgentConfig field. A broken/missing socket disables the feature, not monitoring.
func LocalAvailable() bool {
	parent, e := os.Lstat("/run/monik-console")
	if e != nil || !parent.IsDir() || parent.Mode().Perm()&0022 != 0 {
		return false
	}
	st, ok := parent.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != 0 {
		return false
	}
	i, e := os.Lstat(SocketPath)
	if e != nil || i.Mode()&os.ModeSocket == 0 || i.Mode().Perm() != 0600 {
		return false
	}
	st, ok = i.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == os.Geteuid() && os.Geteuid() != 0
}
func DialLocal(ctx context.Context) (net.Conn, error) {
	if !LocalAvailable() {
		return nil, fmt.Errorf("local console not enabled")
	}
	return (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "unix", SocketPath)
}
