//go:build linux

package spark

import (
	"bufio"
	"bytes"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/processlock"
)

func brokerSocketFixture(t *testing.T, path string) (*net.UnixListener, func() error) {
	t.Helper()
	l, closeSocket, err := listenBrokerSocket(path, uint32(os.Geteuid()), uint32(os.Getegid()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = closeSocket() })
	return l, closeSocket
}

// The child holds the real state lock and socket in another process. Every
// endpoint and state directory belongs only to this test's temporary fixture.
func TestBrokerSocketChild(t *testing.T) {
	if os.Getenv("MONIK_TEST_BROKER_CHILD") != "1" {
		return
	}
	unlock, err := processlock.Acquire(filepath.Join(os.Getenv("MONIK_TEST_BROKER_STATE"), "broker.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	_, closeSocket := brokerSocketFixture(t, os.Getenv("MONIK_TEST_BROKER_SOCKET"))
	fmt.Println("broker-ready")
	var stop [1]byte
	_, _ = os.Stdin.Read(stop[:])
	if err := closeSocket(); err != nil {
		t.Fatal(err)
	}
}

func startBrokerSocketChild(t *testing.T, stateDir, socketPath string) func() {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestBrokerSocketChild$")
	cmd.Env = append(os.Environ(), "MONIK_TEST_BROKER_CHILD=1", "MONIK_TEST_BROKER_STATE="+stateDir, "MONIK_TEST_BROKER_SOCKET="+socketPath)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	wait := make(chan error, 1)
	go func() { wait <- cmd.Wait() }()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			_, _ = stdin.Write([]byte("q"))
			_ = stdin.Close()
			select {
			case err := <-wait:
				if err != nil {
					t.Errorf("broker fixture process: %v: %s", err, stderr.String())
				}
			case <-time.After(4 * time.Second):
				_ = cmd.Process.Kill()
				<-wait
				t.Error("broker fixture process did not stop")
			}
		})
	}
	t.Cleanup(stop)
	ready := make(chan bool, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			if scanner.Text() == "broker-ready" {
				ready <- true
				return
			}
		}
		ready <- false
	}()
	select {
	case ok := <-ready:
		if !ok {
			t.Fatal("broker fixture process failed before listening")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("broker fixture process did not become ready")
	}
	return stop
}

func TestBrokerSocketDifferentStateDirCannotReplacePeer(t *testing.T) {
	dir := t.TempDir()
	firstState, secondState := filepath.Join(dir, "first"), filepath.Join(dir, "second")
	for _, state := range []string{firstState, secondState} {
		if err := os.Mkdir(state, 0700); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(dir, "broker.sock")
	stop := startBrokerSocketChild(t, firstState, path)
	before, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	unlock, err := processlock.Acquire(filepath.Join(secondState, "broker.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if _, closeSocket, err := listenBrokerSocket(path, uint32(os.Geteuid()), uint32(os.Getegid())); err == nil {
		_ = closeSocket()
		t.Fatal("different StateDir replaced the live broker socket")
	}
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(before, after) {
		t.Fatal("duplicate broker changed the live socket inode")
	}
	conn, err := net.DialTimeout("unix", path, time.Second)
	if err != nil {
		t.Fatal("original broker socket became unreachable:", err)
	}
	_ = conn.Close()
	stateLock, err := os.Lstat(filepath.Join(firstState, "broker.lock"))
	if err != nil {
		t.Fatal(err)
	}
	socketLock, err := os.Lstat(path + ".lock")
	if err != nil || socketLock.Mode().Perm() != 0600 {
		t.Fatal("socket lock is not a private regular file")
	}
	stop()
	stateAfter, err := os.Lstat(filepath.Join(firstState, "broker.lock"))
	if err != nil || !os.SameFile(stateLock, stateAfter) {
		t.Fatal("state lock inode was removed or replaced")
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatal("original broker did not remove its own socket")
	}
	_, closeSocket := brokerSocketFixture(t, path)
	if err := closeSocket(); err != nil {
		t.Fatal(err)
	}
	socketLockAfter, err := os.Lstat(path + ".lock")
	if err != nil || !os.SameFile(socketLock, socketLockAfter) {
		t.Fatal("socket lock inode was removed or replaced")
	}
}

func TestBrokerSocketRefusesLivePeerWithoutSocketLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "broker.sock")
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	l.SetUnlinkOnClose(false)
	t.Cleanup(func() { _ = l.Close() })
	before, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, closeSocket, err := listenBrokerSocket(path, uint32(os.Geteuid()), uint32(os.Getegid())); err == nil {
		_ = closeSocket()
		t.Fatal("active legacy socket was unlinked")
	}
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(before, after) {
		t.Fatal("active legacy socket inode changed")
	}
}

func TestBrokerSocketRefusesOccupiedForeignFile(t *testing.T) {
	for _, kind := range []string{"file", "directory", "symlink", "datagram"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "broker.sock")
			switch kind {
			case "file":
				if err := os.WriteFile(path, []byte("foreign data"), 0600); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink("missing-target", path); err != nil {
					t.Fatal(err)
				}
			case "datagram":
				l, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: path, Net: "unixgram"})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = l.Close() })
			}
			before, err := os.Lstat(path)
			if err != nil {
				t.Fatal(err)
			}
			if _, closeSocket, err := listenBrokerSocket(path, uint32(os.Geteuid()), uint32(os.Getegid())); err == nil {
				_ = closeSocket()
				t.Fatal("occupied foreign path was accepted")
			}
			after, err := os.Lstat(path)
			if err != nil || !os.SameFile(before, after) {
				t.Fatal("occupied foreign path was changed")
			}
		})
	}
}

func TestBrokerSocketReplacesVerifiedStaleSocket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "broker.sock")
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	l.SetUnlinkOnClose(false)
	if err = l.Close(); err != nil {
		t.Fatal(err)
	}
	_, closeSocket := brokerSocketFixture(t, path)
	conn, err := net.DialTimeout("unix", path, time.Second)
	if err != nil {
		t.Fatal("verified stale socket was not replaced:", err)
	}
	_ = conn.Close()
	st, err := os.Lstat(path)
	if err != nil || st.Mode().Perm() != 0660 {
		t.Fatal("broker socket permissions changed")
	}
	if err := closeSocket(); err != nil {
		t.Fatal(err)
	}
}

func TestBrokerSocketCleanupPreservesReplacement(t *testing.T) {
	for _, kind := range []string{"file", "socket"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "broker.sock")
			_, closeSocket := brokerSocketFixture(t, path)
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if kind == "file" {
				if err := os.WriteFile(path, []byte("replacement data"), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				l, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
				if err != nil {
					t.Fatal(err)
				}
				l.SetUnlinkOnClose(false)
				t.Cleanup(func() { _ = l.Close() })
			}
			replacement, err := os.Lstat(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := closeSocket(); err != nil {
				t.Fatal(err)
			}
			after, err := os.Lstat(path)
			if err != nil || !os.SameFile(replacement, after) {
				t.Fatal("broker cleanup unlinked replacement inode")
			}
			if kind == "file" {
				data, err := os.ReadFile(path)
				if err != nil || string(data) != "replacement data" {
					t.Fatal("broker cleanup changed replacement file data")
				}
			} else {
				conn, err := net.DialTimeout("unix", path, time.Second)
				if err != nil {
					t.Fatal("replacement listener became unreachable:", err)
				}
				_ = conn.Close()
			}
		})
	}
}

func TestBrokerSocketRefusesUnsafeLock(t *testing.T) {
	for _, kind := range []string{"public", "symlink", "hardlink", "directory"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "broker.sock")
			lock := path + ".lock"
			switch kind {
			case "public":
				if err := os.WriteFile(lock, []byte("foreign lock data"), 0644); err != nil {
					t.Fatal(err)
				}
			case "symlink", "hardlink":
				target := filepath.Join(dir, "target")
				if err := os.WriteFile(target, []byte("foreign lock data"), 0600); err != nil {
					t.Fatal(err)
				}
				var err error
				if kind == "symlink" {
					err = os.Symlink(target, lock)
				} else {
					err = os.Link(target, lock)
				}
				if err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(lock, 0700); err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.Lstat(lock)
			if err != nil {
				t.Fatal(err)
			}
			if _, closeSocket, err := listenBrokerSocket(path, uint32(os.Geteuid()), uint32(os.Getegid())); err == nil {
				_ = closeSocket()
				t.Fatal("unsafe lock allowed socket creation")
			}
			after, err := os.Lstat(lock)
			if err != nil || !os.SameFile(before, after) {
				t.Fatal("unsafe lock inode was changed")
			}
			if _, err := os.Lstat(path); !os.IsNotExist(err) {
				t.Fatal("unsafe lock created a broker socket")
			}
		})
	}
}

func TestBrokerSocketRefusesUnsafeStaleSocket(t *testing.T) {
	for _, kind := range []string{"public-write", "hardlink"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "broker.sock")
			l, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
			if err != nil {
				t.Fatal(err)
			}
			l.SetUnlinkOnClose(false)
			if err = l.Close(); err != nil {
				t.Fatal(err)
			}
			if kind == "public-write" {
				err = os.Chmod(path, 0666)
			} else {
				err = os.Link(path, filepath.Join(dir, "alias.sock"))
			}
			if err != nil {
				t.Fatal(err)
			}
			before, err := os.Lstat(path)
			if err != nil {
				t.Fatal(err)
			}
			if _, closeSocket, err := listenBrokerSocket(path, uint32(os.Geteuid()), uint32(os.Getegid())); err == nil {
				_ = closeSocket()
				t.Fatal("unsafe stale socket was reaped")
			}
			after, err := os.Lstat(path)
			if err != nil || !os.SameFile(before, after) {
				t.Fatal("unsafe stale socket inode was changed")
			}
		})
	}
}

func TestBrokerSocketRefusesUncleanPathOrWrongAuthority(t *testing.T) {
	dir := t.TempDir()
	for _, path := range []string{"relative.sock", dir + "/./broker.sock", filepath.Join(dir, "broker.sock")} {
		owner := uint32(os.Geteuid())
		if path == filepath.Join(dir, "broker.sock") {
			owner++
		}
		if _, closeSocket, err := listenBrokerSocket(path, owner, uint32(os.Getegid())); err == nil {
			_ = closeSocket()
			t.Fatal("invalid path or authority allowed socket creation")
		}
	}
}
