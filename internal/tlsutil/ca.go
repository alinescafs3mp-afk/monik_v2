package tlsutil

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"
)

type Bundle struct {
	CACertPEM []byte
	CAKeyPEM  []byte
	LeafCert  []byte
	LeafKey   []byte
	leaf      atomic.Value // *tls.Certificate
}

func LoadOrCreate(dir string, dns []string, ips []net.IP, leafTTL time.Duration) (*Bundle, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	b := &Bundle{}
	caCrt := filepath.Join(dir, "ca.crt")
	caKey := filepath.Join(dir, "ca.key")
	leafCrt := filepath.Join(dir, "leaf.crt")
	leafKey := filepath.Join(dir, "leaf.key")
	if _, err := os.Stat(caCrt); os.IsNotExist(err) {
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
	needLeaf := false
	if _, err := os.Stat(leafCrt); os.IsNotExist(err) {
		needLeaf = true
	} else {
		certPEM, _ := os.ReadFile(leafCrt)
		block, _ := pem.Decode(certPEM)
		if block != nil {
			c, err := x509.ParseCertificate(block.Bytes)
			if err != nil || time.Until(c.NotAfter) < leafTTL/3 {
				needLeaf = true
			}
		}
	}
	if needLeaf {
		if err := issueLeaf(caCrt, caKey, leafCrt, leafKey, dns, ips, leafTTL); err != nil {
			return nil, err
		}
	}
	if b.LeafCert, err = os.ReadFile(leafCrt); err != nil {
		return nil, err
	}
	if b.LeafKey, err = os.ReadFile(leafKey); err != nil {
		return nil, err
	}
	cert, err := tls.X509KeyPair(b.LeafCert, b.LeafKey)
	if err != nil {
		return nil, err
	}
	b.leaf.Store(&cert)
	return b, nil
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
	certPEM, err := os.ReadFile(filepath.Join(dir, "leaf.crt"))
	if err != nil {
		return err
	}
	keyPEM, err := os.ReadFile(filepath.Join(dir, "leaf.key"))
	if err != nil {
		return err
	}
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return err
	}
	b.LeafCert, b.LeafKey = certPEM, keyPEM
	b.leaf.Store(&cert)
	return nil
}

func (b *Bundle) MaybeRenew(dir string, dns []string, ips []net.IP, leafTTL time.Duration) error {
	block, _ := pem.Decode(b.LeafCert)
	if block == nil {
		return fmt.Errorf("no leaf")
	}
	c, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return err
	}
	if time.Until(c.NotAfter) > leafTTL/3 {
		return nil
	}
	caCrt := filepath.Join(dir, "ca.crt")
	caKey := filepath.Join(dir, "ca.key")
	tmpCrt := filepath.Join(dir, "leaf.crt.new")
	tmpKey := filepath.Join(dir, "leaf.key.new")
	if err := issueLeaf(caCrt, caKey, tmpCrt, tmpKey, dns, ips, leafTTL); err != nil {
		return err
	}
	if err := os.Rename(tmpCrt, filepath.Join(dir, "leaf.crt")); err != nil {
		return err
	}
	if err := os.Rename(tmpKey, filepath.Join(dir, "leaf.key")); err != nil {
		return err
	}
	return b.ReloadLeaf(dir)
}

func (b *Bundle) LeafExpiry() (time.Time, error) {
	block, _ := pem.Decode(b.LeafCert)
	if block == nil {
		return time.Time{}, fmt.Errorf("no leaf")
	}
	c, err := x509.ParseCertificate(block.Bytes)
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
	caCert, err := x509.ParseCertificate(cb.Bytes)
	if err != nil {
		return err
	}
	kb, _ := pem.Decode(caKeyPEM)
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
