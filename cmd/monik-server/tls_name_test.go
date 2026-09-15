package main

import (
	"bytes"
	"github.com/alinescafs3mp-afk/monik_v2/internal/processlock"
	"github.com/alinescafs3mp-afk/monik_v2/internal/tlsutil"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestV14TLSAddNameRequiresStoppedExistingController(t *testing.T) {
	dir := t.TempDir()
	td := filepath.Join(dir, "tls")
	b, e := tlsutil.LoadOrCreate(td, nil, []net.IP{net.ParseIP("127.0.0.1")}, 90*24*time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	ca := append([]byte(nil), b.CACertPEM...)
	before, _ := os.ReadFile(filepath.Join(td, "leaf.bundle.pem"))
	unlock, e := processlock.Acquire(filepath.Join(dir, "controller.lock"))
	if e != nil {
		t.Fatal(e)
	}
	args := []string{"--data-dir", dir, "--name", "46.150.103.61"}
	result := runTLSAddName(args)
	unlock()
	if result == 0 {
		t.Fatal("modified TLS under running owner")
	}
	after, _ := os.ReadFile(filepath.Join(td, "leaf.bundle.pem"))
	if !bytes.Equal(before, after) {
		t.Fatal("locked command changed TLS")
	}
	if result = runTLSAddName(args); result != 0 {
		t.Fatal(result)
	}
	b, e = tlsutil.LoadOrCreate(td, nil, nil, 90*24*time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	c, e := b.LeafCertificate()
	if e != nil || c.VerifyHostname("46.150.103.61") != nil || !bytes.Equal(ca, b.CACertPEM) {
		t.Fatal("name/trust", e)
	}
	if _, e = os.Stat(filepath.Join(dir, "monik.db")); !os.IsNotExist(e) {
		t.Fatal("TLS command should not open database", e)
	}
}
func TestV14TLSAddNameDoesNotBootstrapMissingCA(t *testing.T) {
	dir := t.TempDir()
	if code := runTLSAddName([]string{"--data-dir", dir, "--name", "46.150.103.61"}); code == 0 {
		t.Fatal("missing identity accepted")
	}
	if _, e := os.Stat(filepath.Join(dir, "tls", "ca.crt")); !os.IsNotExist(e) {
		t.Fatal("replacement CA created", e)
	}
}
