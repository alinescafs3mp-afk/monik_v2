package tlsutil

import (
	"crypto/tls"
	"crypto/x509"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestAuditRenewalPreservesSANsAndRecoversFromLegacyPairLoss(t *testing.T) {
	dir := t.TempDir()
	b, err := LoadOrCreate(dir, []string{"monitor.example"}, []net.IP{net.ParseIP("127.0.0.1")}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	oldCA := string(b.CACertPEM)
	if err := b.MaybeRenew(dir, nil, nil, 90*24*time.Hour); err != nil {
		t.Fatal(err)
	}
	crt, err := b.TLSConfig().GetCertificate(&tls.ClientHelloInfo{})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := x509.ParseCertificate(crt.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"monitor.example", "127.0.0.1"} {
		if err := parsed.VerifyHostname(host); err != nil {
			t.Fatalf("SAN lost %s: %v", host, err)
		}
	}
	os.Remove(filepath.Join(dir, "leaf.key"))
	os.WriteFile(filepath.Join(dir, "leaf.crt"), []byte("partial legacy write"), 0600)
	reopened, err := LoadOrCreate(dir, nil, nil, 90*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if string(reopened.CACertPEM) != oldCA {
		t.Fatal("renewal regenerated controller trust")
	}
	info, err := os.Stat(filepath.Join(dir, "leaf.bundle.pem"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("private bundle permissions %v %v", info, err)
	}
}
func TestAuditConcurrentExpiryAndRenewal(t *testing.T) {
	dir := t.TempDir()
	b, err := LoadOrCreate(dir, []string{"monitor.example"}, nil, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				if _, err := b.LeafExpiry(); err != nil {
					t.Error(err)
				}
				if err := b.MaybeRenew(dir, nil, nil, 90*24*time.Hour); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()
}
func TestAuditMalformedCAIsErrorNotPanic(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "ca.crt"), []byte("bad certificate"), 0600)
	os.WriteFile(filepath.Join(dir, "ca.key"), []byte("bad key"), 0600)
	if _, err := LoadOrCreate(dir, nil, nil, time.Hour); err == nil {
		t.Fatal("invalid CA accepted")
	}
}
