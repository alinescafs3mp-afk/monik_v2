//go:build linux

package spark

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"sync"
	"syscall"
)

type codeHashEntry struct {
	key  string
	hash string
}

var codeHashes = struct {
	sync.Mutex
	items map[string]codeHashEntry
}{items: map[string]codeHashEntry{}}

// Protected code is streamed with a fixed buffer. Cache hits require the same
// device/inode/size/mtime/ctime and still revalidate path ownership/modes.
func protectedCodeHash(path string, owner uint32) (string, error) {
	if e := trustedPath(path, owner, false); e != nil {
		return "", e
	}
	fd, e := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if e != nil {
		return "", e
	}
	file := os.NewFile(uintptr(fd), path)
	defer file.Close()
	info, e := file.Stat()
	if e != nil {
		return "", e
	}
	native, ok := info.Sys().(*syscall.Stat_t)
	if !ok || native.Uid != owner || !info.Mode().IsRegular() || info.Size() > 1<<30 {
		return "", fmt.Errorf("protected code type/size invalid")
	}
	key := codeStatKey(native)
	codeHashes.Lock()
	cached, exists := codeHashes.items[path]
	codeHashes.Unlock()
	if exists && cached.key == key {
		return cached.hash, nil
	}
	hasher := sha256.New()
	if _, e = io.CopyBuffer(hasher, io.LimitReader(file, 1<<30+1), make([]byte, 32<<10)); e != nil {
		return "", e
	}
	after, e := file.Stat()
	if e != nil {
		return "", e
	}
	if codeStatKey(after.Sys().(*syscall.Stat_t)) != key {
		return "", fmt.Errorf("protected code changed while hashing")
	}
	hash := hex.EncodeToString(hasher.Sum(nil))
	codeHashes.Lock()
	if len(codeHashes.items) >= 128 {
		for old := range codeHashes.items {
			delete(codeHashes.items, old)
			break
		}
	}
	codeHashes.items[path] = codeHashEntry{key: key, hash: hash}
	codeHashes.Unlock()
	return hash, nil
}
func codeStatKey(s *syscall.Stat_t) string {
	return fmt.Sprintf("%d:%d:%d:%d:%d:%d:%d", s.Dev, s.Ino, s.Size, s.Mtim.Sec, s.Mtim.Nsec, s.Ctim.Sec, s.Ctim.Nsec)
}
