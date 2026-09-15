package tlsutil

import (
	"crypto/tls"
	"crypto/x509"
	"net"
	"testing"
	"time"
)

func TestV14RegressRenewalWithNewNamesRetainsOldSANs(t *testing.T) {
	dir := t.TempDir()
	b, e := LoadOrCreate(dir, []string{"old.example"}, []net.IP{net.ParseIP("192.168.12.128")}, time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	oldCA := string(b.CACertPEM)
	if e = b.MaybeRenew(dir, []string{"new.example"}, []net.IP{net.ParseIP("46.150.103.61")}, 90*24*time.Hour); e != nil {
		t.Fatal(e)
	}
	c, e := b.TLSConfig().GetCertificate(&tls.ClientHelloInfo{})
	if e != nil {
		t.Fatal(e)
	}
	leaf, e := x509.ParseCertificate(c.Certificate[0])
	if e != nil {
		t.Fatal(e)
	}
	for _, h := range []string{"old.example", "new.example", "192.168.12.128", "46.150.103.61"} {
		if e = leaf.VerifyHostname(h); e != nil {
			t.Errorf("lost old or new SAN %s: %v", h, e)
		}
	}
	if string(b.CACertPEM) != oldCA {
		t.Fatal("CA changed")
	}
}
