package tlsutil

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestV12TLSIdentityRejectsSwappedKeyAndUnrelatedLeaf(t *testing.T) {
	for _, part := range []string{"key", "root-and-key", "missing-root-pair"} {
		t.Run(part, func(t *testing.T) {
			dir := t.TempDir()
			other := t.TempDir()
			a, e := LoadOrCreate(dir, []string{"localhost"}, []net.IP{net.ParseIP("127.0.0.1")}, 24*time.Hour)
			if e != nil {
				t.Fatal(e)
			}
			b, e := LoadOrCreate(other, nil, nil, 24*time.Hour)
			if e != nil {
				t.Fatal(e)
			}
			if part == "missing-root-pair" {
				os.Remove(filepath.Join(dir, "ca.crt"))
				os.Remove(filepath.Join(dir, "ca.key"))
			} else {
				if e = os.WriteFile(filepath.Join(dir, "ca.key"), b.CAKeyPEM, 0600); e != nil {
					t.Fatal(e)
				}
				if part == "root-and-key" {
					if e = os.WriteFile(filepath.Join(dir, "ca.crt"), b.CACertPEM, 0644); e != nil {
						t.Fatal(e)
					}
				}
			}
			if _, e = LoadOrCreate(dir, nil, nil, 24*time.Hour); e == nil {
				t.Fatal("inconsistent enrolled TLS identity accepted")
			}
			if part == "missing-root-pair" {
				if _, e = os.Stat(filepath.Join(dir, "ca.key")); !os.IsNotExist(e) {
					t.Fatal("regenerated a lost root over an existing leaf")
				}
			}
			_ = a
		})
	}
}
