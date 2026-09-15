//go:build linux

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestV15ProtectedReaderRejectsLinksAndPublicCredentials(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "credential")
	if e := os.WriteFile(file, []byte("fixture-secret"), 0600); e != nil {
		t.Fatal(e)
	}
	if b, e := readPrivateFile(file, 4096); e != nil || string(b) != "fixture-secret" {
		t.Fatal(e)
	}
	link := filepath.Join(root, "alias")
	os.Symlink(file, link)
	if _, e := readPrivateFile(link, 4096); e == nil {
		t.Fatal("file symlink read")
	}
	dirlink := filepath.Join(root, "dirlink")
	os.Symlink(root, dirlink)
	if _, e := readPrivateFile(filepath.Join(dirlink, "credential"), 4096); e == nil {
		t.Fatal("parent symlink read")
	}
	hard := filepath.Join(root, "hard")
	os.Link(file, hard)
	if _, e := readPrivateFile(file, 4096); e == nil {
		t.Fatal("hardlinked credential read")
	}
	os.Remove(hard)
	os.Chmod(file, 0644)
	if _, e := readPrivateFile(file, 4096); e == nil {
		t.Fatal("public credential read")
	}
}
func TestV15ExecutableDigestReadsOnlyBoundedRegularUnlinkedPath(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "worker")
	data := []byte("test worker")
	os.WriteFile(p, data, 0700)
	sum := sha256.Sum256(data)
	if got, e := fileDigest(p); e != nil || got != hex.EncodeToString(sum[:]) {
		t.Fatal(got, e)
	}
	if _, e := fileDigest(root); e == nil {
		t.Fatal("directory digest")
	}
	os.Symlink(p, filepath.Join(root, "alias"))
	if _, e := fileDigest(filepath.Join(root, "alias")); e == nil {
		t.Fatal("linked executable accepted")
	}
	if _, e := readPrivateFile(p, 2); e == nil {
		t.Fatal("unbounded read")
	}
	if _, e := openNoLinks("relative"); e == nil {
		t.Fatal("relative protected path")
	}
}
