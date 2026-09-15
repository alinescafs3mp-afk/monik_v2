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
	"strings"
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
		for _, existing := range []string{caKey, filepath.Join(dir, "leaf.bundle.pem"), filepath.Join(dir, "leaf.crt"), filepath.Join(dir, "leaf.key")} {
			if _, e := os.Lstat(existing); e == nil || !os.IsNotExist(e) {
				return nil, fmt.Errorf("CA certificate missing while TLS state exists; restore the matching CA")
			}
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
	// Validate the root and its private key even when the leaf is not due for
	// renewal. Otherwise a mismatched replacement is only detected months later.
	rootPair, err := tls.X509KeyPair(b.CACertPEM, b.CAKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("controller CA key/certificate mismatch: %w", err)
	}
	root, err := x509.ParseCertificate(rootPair.Certificate[0])
	if err != nil {
		return nil, err
	}
	if !root.IsCA || !root.BasicConstraintsValid || time.Now().Before(root.NotBefore) || !time.Now().Before(root.NotAfter) {
		return nil, fmt.Errorf("controller CA is not currently valid; restore identity or rotate trust")
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
	leafCert, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return nil, err
	}
	if err := leafCert.CheckSignatureFrom(root); err != nil {
		return nil, fmt.Errorf("stored leaf is not signed by the enrolled CA: %w", err)
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
	// New settings/interfaces may add SANs; ordinary renewal must not drop old ones.
	dns = append(append([]string(nil), c.DNSNames...), dns...)
	ips = append(append([]net.IP(nil), c.IPAddresses...), ips...)
	return b.replaceLeaf(dir, dns, ips, leafTTL)
}

func (b *Bundle) replaceLeaf(dir string, dns []string, ips []net.IP, leafTTL time.Duration) error {
	// Renewal runs repeatedly; preserve names without growing duplicate SANs.
	uniqueDNS := make([]string, 0, len(dns))
	seenDNS := map[string]bool{}
	for _, name := range dns {
		key := strings.ToLower(name)
		if !seenDNS[key] {
			uniqueDNS = append(uniqueDNS, name)
			seenDNS[key] = true
		}
	}
	uniqueIPs := make([]net.IP, 0, len(ips))
	seenIP := map[string]bool{}
	for _, ip := range ips {
		if ip == nil {
			return fmt.Errorf("invalid nil IP SAN")
		}
		key := ip.String()
		if !seenIP[key] {
			uniqueIPs = append(uniqueIPs, ip)
			seenIP[key] = true
		}
	}
	dns, ips = uniqueDNS, uniqueIPs
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

// LeafCertificate parses a snapshot of the active certificate. It exposes no key.
func (b *Bundle) LeafCertificate() (*x509.Certificate, error) {
	active, ok := b.leaf.Load().(*tls.Certificate)
	if !ok || len(active.Certificate) == 0 {
		return nil, fmt.Errorf("no active certificate")
	}
	return x509.ParseCertificate(active.Certificate[0])
}

// AddServerName is an explicit OFFLINE administrative action. Existing CA and
// all previous SANs are preserved. It neither changes routes nor migrates agents.
func (b *Bundle) AddServerName(dir, name string) (bool, error) {
	if net.ParseIP(name) == nil {
		if len(name) == 0 || len(name) > 253 || strings.ContainsAny(name, "/:*% \t\r\n") {
			return false, fmt.Errorf("name must be an IP address or DNS hostname, not a URL")
		}
		for _, label := range strings.Split(name, ".") {
			if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
				return false, fmt.Errorf("invalid DNS name")
			}
			for _, c := range label {
				if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
					return false, fmt.Errorf("DNS name must be ASCII; use its IDNA form")
				}
			}
		}
	}
	b.renewMu.Lock()
	defer b.renewMu.Unlock()
	c, err := b.LeafCertificate()
	if err != nil {
		return false, err
	}
	if c.VerifyHostname(name) == nil {
		return false, nil
	}
	dns := append([]string(nil), c.DNSNames...)
	ips := append([]net.IP(nil), c.IPAddresses...)
	if ip := net.ParseIP(name); ip != nil {
		ips = append(ips, ip)
	} else {
		dns = append(dns, name)
	}
	if err := b.replaceLeaf(dir, dns, ips, 90*24*time.Hour); err != nil {
		return false, err
	}
	return true, nil
}
