package tlsutil

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

type Bundle struct {
	renewMu   sync.Mutex
	CACertPEM []byte
	CAKeyPEM  []byte
	LeafCert  []byte
	LeafKey   []byte
	leaf      atomic.Value // *tls.Certificate
}

func LoadOrCreate(dir string, dns []string, ips []net.IP, leafTTL time.Duration) (*Bundle, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	b := &Bundle{}
	caCrt, caKey := filepath.Join(dir, "ca.crt"), filepath.Join(dir, "ca.key")
	if _, err := os.Stat(caCrt); os.IsNotExist(err) {
		// Never overwrite an existing half of a controller identity.
		if _, keyErr := os.Stat(caKey); keyErr == nil {
			return nil, fmt.Errorf("CA certificate missing while private key exists; restore the matching CA")
		}
		if err := generateCA(caCrt, caKey); err != nil {
			return nil, err
		}
	}
	var err error
	if b.CACertPEM, err = os.ReadFile(caCrt); err != nil {
		return nil, err
	}
	if b.CAKeyPEM, err = os.ReadFile(caKey); err != nil {
		return nil, err
	}
	leaf, key, err := readLeafPair(dir)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if os.IsNotExist(err) {
		if err := issueLeaf(caCrt, caKey, filepath.Join(dir, "leaf.crt"), filepath.Join(dir, "leaf.key"), dns, ips, leafTTL); err != nil {
			return nil, err
		}
		leaf, key, err = readLeafPair(dir)
		if err != nil {
			return nil, err
		}
	}
	cert, err := tls.X509KeyPair(leaf, key)
	if err != nil {
		return nil, err
	}
	b.LeafCert, b.LeafKey = leaf, key
	b.leaf.Store(&cert)
	// Migrate the legacy two-file pair only after validating it.
	if _, err := os.Stat(filepath.Join(dir, "leaf.bundle.pem")); os.IsNotExist(err) {
		if err := writeLeafBundle(dir, leaf, key); err != nil {
			return nil, err
		}
	}
	if err := b.MaybeRenew(dir, dns, ips, leafTTL); err != nil {
		return nil, err
	}
	return b, nil
}

func readLeafPair(dir string) ([]byte, []byte, error) {
	if bundle, err := os.ReadFile(filepath.Join(dir, "leaf.bundle.pem")); err == nil {
		return bundle, bundle, nil
	} else if !os.IsNotExist(err) {
		return nil, nil, err
	}
	cert, err := os.ReadFile(filepath.Join(dir, "leaf.crt"))
	if err != nil {
		return nil, nil, err
	}
	key, err := os.ReadFile(filepath.Join(dir, "leaf.key"))
	return cert, key, err
}

func writeLeafBundle(dir string, cert, key []byte) error {
	if _, err := tls.X509KeyPair(cert, key); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".leaf-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	// The bundle contains private key material: CreateTemp creates mode 0600.
	if _, err = tmp.Write(append(append([]byte{}, cert...), key...)); err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(tmp.Name(), filepath.Join(dir, "leaf.bundle.pem")); err != nil {
		return err
	}
	if d, e := os.Open(dir); e == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

func (b *Bundle) TLSConfig() *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			c := b.leaf.Load().(*tls.Certificate)
			return c, nil
		},
	}
}

func (b *Bundle) ReloadLeaf(dir string) error {
	certPEM, keyPEM, err := readLeafPair(dir)
	if err != nil {
		return err
	}
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return err
	}
	// The atomic certificate, not mutable exported byte slices, is authoritative.
	b.leaf.Store(&cert)
	return nil
}

func (b *Bundle) MaybeRenew(dir string, dns []string, ips []net.IP, leafTTL time.Duration) error {
	b.renewMu.Lock()
	defer b.renewMu.Unlock()
	if leafTTL <= 0 {
		leafTTL = 90 * 24 * time.Hour
	}
	active, ok := b.leaf.Load().(*tls.Certificate)
	if !ok || len(active.Certificate) == 0 {
		return fmt.Errorf("no leaf")
	}
	c, err := x509.ParseCertificate(active.Certificate[0])
	if err != nil {
		return err
	}
	if time.Until(c.NotAfter) > leafTTL/3 {
		return nil
	}
	// Ordinary renewal cannot silently delete the enrolled IP/DNS identities.
	if len(dns) == 0 {
		dns = c.DNSNames
	}
	if len(ips) == 0 {
		ips = c.IPAddresses
	}
	staging, err := os.MkdirTemp(dir, ".renew-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)
	certPath, keyPath := filepath.Join(staging, "leaf.crt"), filepath.Join(staging, "leaf.key")
	if err := issueLeaf(filepath.Join(dir, "ca.crt"), filepath.Join(dir, "ca.key"), certPath, keyPath, dns, ips, leafTTL); err != nil {
		return err
	}
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return err
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return err
	}
	if err := writeLeafBundle(dir, certPEM, keyPEM); err != nil {
		return err
	}
	return b.ReloadLeaf(dir)
}

func (b *Bundle) LeafExpiry() (time.Time, error) {
	active, ok := b.leaf.Load().(*tls.Certificate)
	if !ok || len(active.Certificate) == 0 {
		return time.Time{}, fmt.Errorf("no leaf")
	}
	c, err := x509.ParseCertificate(active.Certificate[0])
	if err != nil {
		return time.Time{}, err
	}
	return c.NotAfter, nil
}

func generateCA(certPath, keyPath string) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "Monik Controller CA", Organization: []string{"Monik"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            1,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return err
	}
	if err := writePEM(certPath, "CERTIFICATE", der, 0o644); err != nil {
		return err
	}
	b, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	return writePEM(keyPath, "EC PRIVATE KEY", b, 0o600)
}

func issueLeaf(caCrt, caKey, leafCrt, leafKey string, dns []string, ips []net.IP, ttl time.Duration) error {
	caPEM, err := os.ReadFile(caCrt)
	if err != nil {
		return err
	}
	caKeyPEM, err := os.ReadFile(caKey)
	if err != nil {
		return err
	}
	cb, _ := pem.Decode(caPEM)
	if cb == nil {
		return fmt.Errorf("invalid CA certificate PEM")
	}
	caCert, err := x509.ParseCertificate(cb.Bytes)
	if err != nil {
		return err
	}
	kb, _ := pem.Decode(caKeyPEM)
	if kb == nil {
		return fmt.Errorf("invalid CA key PEM")
	}
	caPriv, err := x509.ParseECPrivateKey(kb.Bytes)
	if err != nil {
		return err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if ttl <= 0 {
		ttl = 90 * 24 * time.Hour
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "monik-controller", Organization: []string{"Monik"}},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(ttl),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     dns,
		IPAddresses:  ips,
	}
	if !caCert.NotAfter.After(time.Now()) {
		return fmt.Errorf("controller CA expired; trust rotation is required")
	}
	if tmpl.NotAfter.After(caCert.NotAfter) {
		tmpl.NotAfter = caCert.NotAfter
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caPriv)
	if err != nil {
		return err
	}
	if err := writePEM(leafCrt, "CERTIFICATE", der, 0o644); err != nil {
		return err
	}
	b, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	return writePEM(leafKey, "EC PRIVATE KEY", b, 0o600)
}

func writePEM(path, typ string, der []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer f.Close()
	return pem.Encode(f, &pem.Block{Type: typ, Bytes: der})
}

func PoolFromPEM(pemBytes []byte) (*x509.CertPool, error) {
	p := x509.NewCertPool()
	if !p.AppendCertsFromPEM(pemBytes) {
		return nil, fmt.Errorf("invalid CA PEM")
	}
	return p, nil
}

func CertFingerprint(pemBytes []byte) (string, error) {
	certs, err := ParseCerts(pemBytes)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(certs[0].Raw)
	return hex.EncodeToString(sum[:]), nil
}

func ParseCerts(pemBytes []byte) ([]*x509.Certificate, error) {
	var out []*x509.Certificate
	rest := pemBytes
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		c, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no certificate in PEM")
	}
	return out, nil
}

func FingerprintDER(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
