package tlsutil

import (
	"bytes"
	"crypto/x509"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestV14AddNamePreservesCAAndEveryPriorRoute(t *testing.T) {
	dir := t.TempDir()
	b, e := LoadOrCreate(dir, []string{"lan.example"}, []net.IP{net.ParseIP("192.168.12.128"), net.ParseIP("127.0.0.1")}, 90*24*time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	ca := append([]byte(nil), b.CACertPEM...)
	key, _ := os.ReadFile(filepath.Join(dir, "ca.key"))
	before, _ := os.ReadFile(filepath.Join(dir, "leaf.bundle.pem"))
	changed, e := b.AddServerName(dir, "46.150.103.61")
	if e != nil || !changed {
		t.Fatal(changed, e)
	}
	roots, e := PoolFromPEM(ca)
	if e != nil {
		t.Fatal(e)
	}
	c, e := b.LeafCertificate()
	if e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"lan.example", "192.168.12.128", "127.0.0.1", "46.150.103.61"} {
		if _, e := c.Verify(x509.VerifyOptions{Roots: roots, DNSName: name}); e != nil {
			t.Fatal(name, e)
		}
	}
	after, _ := os.ReadFile(filepath.Join(dir, "leaf.bundle.pem"))
	if bytes.Equal(before, after) {
		t.Fatal("name was not published")
	}
	changed, e = b.AddServerName(dir, "46.150.103.61")
	if e != nil || changed {
		t.Fatal("non-idempotent", changed, e)
	}
	again, _ := os.ReadFile(filepath.Join(dir, "leaf.bundle.pem"))
	if !bytes.Equal(after, again) {
		t.Fatal("no-op rewrote key/cert")
	}
	ca2, _ := os.ReadFile(filepath.Join(dir, "ca.crt"))
	key2, _ := os.ReadFile(filepath.Join(dir, "ca.key"))
	if !bytes.Equal(ca, ca2) || !bytes.Equal(key, key2) {
		t.Fatal("identity replaced")
	}
	reloaded, e := LoadOrCreate(dir, nil, nil, 90*24*time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	leaf, _ := reloaded.LeafCertificate()
	if e = leaf.VerifyHostname("46.150.103.61"); e != nil {
		t.Fatal(e)
	}
}
func TestV14InvalidTLSNameDoesNotWrite(t *testing.T) {
	dir := t.TempDir()
	b, e := LoadOrCreate(dir, nil, []net.IP{net.ParseIP("127.0.0.1")}, time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	before, _ := os.ReadFile(filepath.Join(dir, "leaf.bundle.pem"))
	for _, bad := range []string{"", "https://46.150.103.61:8777/", "abc/path", "*.example", "a..b", "-example.test", "a\nb", "[::1]", "a%0a", "имя.рф"} {
		if _, e = b.AddServerName(dir, bad); e == nil {
			t.Fatal("accepted", bad)
		}
	}
	after, _ := os.ReadFile(filepath.Join(dir, "leaf.bundle.pem"))
	if !bytes.Equal(before, after) {
		t.Fatal("invalid name changed leaf")
	}
}
func TestV14RenewalDoesNotAccumulateDuplicateSANs(t *testing.T) {
	dir := t.TempDir()
	b, e := LoadOrCreate(dir, []string{"host.example"}, []net.IP{net.ParseIP("127.0.0.1")}, time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	if e = b.MaybeRenew(dir, []string{"host.example", "HOST.EXAMPLE"}, []net.IP{net.ParseIP("127.0.0.1"), net.IPv4(127, 0, 0, 1)}, 90*24*time.Hour); e != nil {
		t.Fatal(e)
	}
	leaf, e := b.LeafCertificate()
	if e != nil || len(leaf.DNSNames) != 1 || len(leaf.IPAddresses) != 1 {
		t.Fatal(leaf, e)
	}
}
