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
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestV18PTYInputResizeAndClose(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SHOULD_NOT_REACH_TERMINAL", "fixture-secret")
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- ServeLocal(ctx, b, "fixture") }()
	l := NewLocal(a)
	id := strings.Repeat("a", 64)
	a.SetDeadline(time.Now().Add(8 * time.Second))
	if e := l.Send(Message{Type: "open", Session: id, Cols: 80, Rows: 24}); e != nil {
		t.Fatal(e)
	}
	ready, e := l.Read()
	if e != nil || ready.Type != "ready" {
		t.Fatal(ready, e)
	}
	if e = l.Send(Message{Type: "resize", Session: id, Cols: 101, Rows: 31, Seq: 1}); e != nil {
		t.Fatal(e)
	}
	if e = l.Send(Message{Type: "input", Session: id, Data: "printf '\\nMARK_%s\\n' 123; stty size; printf '%s' \"$SHOULD_NOT_REACH_TERMINAL\"\n", Seq: 2}); e != nil {
		t.Fatal(e)
	}
	text := ""
	for !strings.Contains(text, "MARK_123") || !strings.Contains(text, "31 101") {
		m, e := l.Read()
		if e != nil {
			t.Fatal(e, text)
		}
		if m.Type == "output" {
			b, e := base64.StdEncoding.DecodeString(m.Data)
			if e != nil {
				t.Fatal(e)
			}
			text += string(b)
		}
	}
	if strings.Contains(text, "fixture-secret") {
		t.Fatal("inherited sensitive environment")
	}
	a.Close()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("PTY did not close on transport loss")
	}
}
func TestV18LocalInfoDoesNotCreateShell(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	done := make(chan error, 1)
	go func() { done <- ServeLocal(context.Background(), b, "monik-console") }()
	l := NewLocal(a)
	if e := l.Send(Message{Type: "info"}); e != nil {
		t.Fatal(e)
	}
	m, e := l.Read()
	if e != nil || m != (Message{Type: "info", User: "monik-console"}) {
		t.Fatal(m, e)
	}
	if e = <-done; e != nil {
		t.Fatal(e)
	}
}
func TestV18ActivatedChild(t *testing.T) {
	role := os.Getenv("MONIK_V18_CHILD")
	if role == "" {
		t.Skip("subprocess fixture only")
	}
	if role == "broker" {
		uid, _ := strconv.Atoi(os.Getenv("MONIK_V18_PEER"))
		if e := ServeActivated(context.Background(), uid); e != nil {
			fmt.Fprintln(os.Stderr, e)
			os.Exit(8)
		}
		return
	}
	c, e := net.Dial("unix", os.Getenv("MONIK_V18_SOCKET"))
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(8 * time.Second))
	l := NewLocal(c)
	id := strings.Repeat("d", 64)
	if e = l.Send(Message{Type: "open", Session: id, Cols: 80, Rows: 24}); e != nil {
		t.Fatal(e)
	}
	m, e := l.Read()
	if os.Getenv("MONIK_V18_DENY") == "1" {
		if e == nil {
			t.Fatal("wrong peer accepted", m)
		}
		return
	}
	if e != nil || m.Type != "ready" {
		t.Fatal(m, e)
	}
	// Kernel UID and no_new_privs must really be in force in the executed shell.
	cmd := "printf '\\nUSER_%s\\n' \"$(id -u)\"; grep NoNewPrivs /proc/self/status; test ! -r '" + os.Getenv("MONIK_V18_SECRET") + "' && printf 'KEY_%s\\n' isolated; printf 'ENV_%s\\n' \"${SHOULD_NOT_REACH_TERMINAL-unset}\"\n"
	if e = l.Send(Message{Type: "input", Session: id, Data: cmd, Seq: 1}); e != nil {
		t.Fatal(e)
	}
	text := ""
	for !strings.Contains(text, "USER_65534") || !strings.Contains(text, "KEY_isolated") || !strings.Contains(text, "ENV_unset") || !strings.Contains(text, "NoNewPrivs:\t1") {
		m, e = l.Read()
		if e != nil {
			t.Fatal(e, text)
		}
		if m.Type == "output" {
			b, _ := OutputBytes(m)
			text += string(b)
		}
	}
	c.Close()
}

// Exercises the real peer-credential entrypoint and OS UID separation; does not
// claim to emulate the systemd mount namespace, cgroup lifecycle or boot.
func TestV18NativeActivatedPeerUIDAndKeyIsolation(t *testing.T) {
	if os.Getenv("MONIK_NATIVE_TESTS") != "1" || os.Geteuid() != 0 {
		t.Skip("requires explicit native process tests and root to drop fixture UIDs")
	}
	for _, expected := range []int{1, 2} {
		t.Run(fmt.Sprint(expected), func(t *testing.T) {
			dir, e := os.MkdirTemp("", "monik-v18-")
			if e != nil {
				t.Fatal(e)
			}
			defer os.RemoveAll(dir)
			os.Chmod(dir, 0755)
			bin := filepath.Join(dir, "test")
			raw, e := os.ReadFile(os.Args[0])
			if e != nil {
				t.Fatal(e)
			}
			if e = os.WriteFile(bin, raw, 0755); e != nil {
				t.Fatal(e)
			}
			secret := filepath.Join(dir, "monitoring-key")
			os.WriteFile(secret, []byte("protected-fixture-credential"), 0600)
			os.Chown(secret, 1, 1)
			home := filepath.Join(dir, "home")
			os.Mkdir(home, 0700)
			os.Chown(home, 65534, 65534)
			path := filepath.Join(dir, "s")
			ln, e := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
			if e != nil {
				t.Fatal(e)
			}
			defer ln.Close()
			os.Chmod(path, 0600)
			os.Chown(path, 1, 1)
			cl := exec.Command(bin, "-test.run=^TestV18ActivatedChild$")
			cl.Env = []string{"MONIK_V18_CHILD=client", "MONIK_V18_SOCKET=" + path, "MONIK_V18_SECRET=" + secret}
			if expected != 1 {
				cl.Env = append(cl.Env, "MONIK_V18_DENY=1")
			}
			cl.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: 1, Gid: 1}}
			var log strings.Builder
			cl.Stdout = &log
			cl.Stderr = &log
			if e = cl.Start(); e != nil {
				t.Fatal(e)
			}
			defer cl.Process.Kill()
			ln.SetDeadline(time.Now().Add(5 * time.Second))
			accepted, e := ln.AcceptUnix()
			if e != nil {
				t.Fatal(e)
			}
			defer accepted.Close()
			f, e := accepted.File()
			if e != nil {
				t.Fatal(e)
			}
			defer f.Close()
			broker := exec.Command(bin, "-test.run=^TestV18ActivatedChild$")
			broker.Stdin = f
			broker.Env = []string{"MONIK_V18_CHILD=broker", "MONIK_V18_PEER=" + strconv.Itoa(expected), "HOME=" + home, "SHOULD_NOT_REACH_TERMINAL=fixture-secret"}
			broker.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: 65534, Gid: 65534}}
			var blog strings.Builder
			broker.Stdout = &blog
			broker.Stderr = &blog
			if e = broker.Start(); e != nil {
				t.Fatal(e)
			}
			defer broker.Process.Kill()
			accepted.Close()
			f.Close()
			clientDone := make(chan error, 1)
			go func() { clientDone <- cl.Wait() }()
			select {
			case e = <-clientDone:
				if e != nil {
					t.Fatal(e, log.String(), blog.String())
				}
			case <-time.After(12 * time.Second):
				t.Fatal("native client timed out")
			}
			brokerDone := make(chan error, 1)
			go func() { brokerDone <- broker.Wait() }()
			select {
			case e = <-brokerDone:
				if expected == 1 && e != nil {
					t.Fatal(e, blog.String())
				}
				if expected != 1 && e == nil {
					t.Fatal("wrong UID not rejected")
				}
			case <-time.After(4 * time.Second):
				t.Fatal("broker remained alive")
			}
			u, _ := user.LookupId("65534")
			t.Logf("peer uid=1; broker uid=65534 (%s); expected peer=%d; permission and cleanup verified", u.Username, expected)
		})
	}
}
