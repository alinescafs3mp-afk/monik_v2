package installerbundle

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"strings"
	"testing"
	"time"
)

func testELF(machine uint16) []byte {
	b := make([]byte, 128)
	copy(b, []byte{0x7f, 'E', 'L', 'F', 2, 1, 1})
	binary.LittleEndian.PutUint16(b[16:], 2)
	binary.LittleEndian.PutUint16(b[18:], machine)
	binary.LittleEndian.PutUint32(b[20:], 1)
	binary.LittleEndian.PutUint16(b[52:], 64)
	return b
}
func testTemplate(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if e := Pack(&b, testELF(62), testELF(62), testELF(62), "amd64", "fixture"); e != nil {
		t.Fatal(e)
	}
	return b.Bytes()
}
func testProfile(t *testing.T) *Profile {
	t.Helper()
	pub, priv, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now()
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Fixture CA"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, e := x509.CreateCertificate(rand.Reader, cert, cert, pub, priv)
	if e != nil {
		t.Fatal(e)
	}
	return &Profile{ControllerURL: "https://127.0.0.1:8777", ControllerID: "fixture-controller", CACertPEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), EnrollmentCode: strings.Repeat("A", 32), ExpiresAt: now.Add(time.Hour).UTC()}
}
func TestV15TemplateAndBoundProfileRoundtrip(t *testing.T) {
	raw := testTemplate(t)
	b, e := Open(bytes.NewReader(raw), int64(len(raw)))
	if e != nil {
		t.Fatal(e)
	}
	if b.Manifest.Profile != nil {
		t.Fatal("template contains credential")
	}
	p := testProfile(t)
	var out bytes.Buffer
	n, e := b.Write(&out, p)
	if e != nil {
		t.Fatal(e)
	}
	if n != b.SizeWith(p) || n != int64(out.Len()) {
		t.Fatal("length mismatch")
	}
	bound, e := Open(bytes.NewReader(out.Bytes()), n)
	if e != nil {
		t.Fatal(e)
	}
	if bound.Manifest.Profile.EnrollmentCode != p.EnrollmentCode {
		t.Fatal("profile lost")
	}
	if !bytes.Equal(raw[:b.PayloadSize], out.Bytes()[:b.PayloadSize]) {
		t.Fatal("payload changed while binding")
	}
	var clean bytes.Buffer
	_, e = bound.Write(&clean, nil)
	if e != nil || !bytes.Equal(raw, clean.Bytes()) {
		t.Fatal("template not reproducible")
	}
}
func TestV15ExtractExactlyVerifiedPrograms(t *testing.T) {
	raw := testTemplate(t)
	b, _ := Open(bytes.NewReader(raw), int64(len(raw)))
	var w, h bytes.Buffer
	if e := b.CopyWorker(&w); e != nil {
		t.Fatal(e)
	}
	if e := b.CopySupervisor(&h); e != nil {
		t.Fatal(e)
	}
	if !bytes.Equal(w.Bytes(), testELF(62)) || !bytes.Equal(w.Bytes(), h.Bytes()) {
		t.Fatal("incorrect payload extraction")
	}
}
func TestV15RejectsCorruptionInEveryPart(t *testing.T) {
	raw := testTemplate(t)
	for _, at := range []int{70, 150, 300, len(raw) - 1} {
		t.Run(string(rune(at)), func(t *testing.T) {
			b := append([]byte(nil), raw...)
			b[at] ^= 1
			if _, e := Open(bytes.NewReader(b), int64(len(b))); e == nil {
				t.Fatal("tampering accepted")
			}
		})
	}
}
func TestV15RejectsTruncatedOrHugeFooter(t *testing.T) {
	raw := testTemplate(t)
	for _, n := range []int{0, 10, 55, len(raw) - 1} {
		if _, e := Open(bytes.NewReader(raw[:n]), int64(n)); e == nil {
			t.Fatal("truncation accepted")
		}
	}
	binary.BigEndian.PutUint64(raw[len(raw)-40:], ^uint64(0))
	if _, e := Open(bytes.NewReader(raw), int64(len(raw))); e == nil {
		t.Fatal("huge manifest accepted")
	}
	if _, e := Open(bytes.NewReader(nil), MaxFile+1); e == nil {
		t.Fatal("huge file accepted")
	}
}
func rewriteManifest(t *testing.T, raw []byte, edit func([]byte) []byte) []byte {
	t.Helper()
	b, e := Open(bytes.NewReader(raw), int64(len(raw)))
	if e != nil {
		t.Fatal(e)
	}
	m := edit(append([]byte(nil), raw[b.PayloadSize:len(raw)-footerSize]...))
	result := append(append([]byte(nil), raw[:b.PayloadSize]...), m...)
	tail := make([]byte, footerSize)
	copy(tail, Magic)
	binary.BigEndian.PutUint64(tail[16:24], uint64(len(m)))
	sum := sha256.Sum256(m)
	copy(tail[24:], sum[:])
	return append(result, tail...)
}
func TestV15RejectsAmbiguousAndUnknownManifest(t *testing.T) {
	for _, edit := range []func([]byte) []byte{
		func(b []byte) []byte {
			return bytes.Replace(b, []byte(`"format":1`), []byte(`"format":0,"format":1`), 1)
		},
		func(b []byte) []byte {
			return bytes.Replace(b, []byte(`"format":1`), []byte(`"format":1,"shell":"bad"`), 1)
		},
		func(b []byte) []byte { return append(b, []byte(` {}`)...) },
		func(b []byte) []byte { return bytes.Replace(b, []byte(`"size":128`), []byte(`"size":-1`), 1) },
	} {
		raw := rewriteManifest(t, testTemplate(t), edit)
		if _, e := Open(bytes.NewReader(raw), int64(len(raw))); e == nil {
			t.Fatal("invalid manifest accepted")
		}
	}
}
func TestV15RejectsWrongArchitectureAndScripts(t *testing.T) {
	for _, p := range [][]byte{[]byte("#!/bin/sh\necho unsafe"), testELF(183)} {
		var out bytes.Buffer
		if e := Pack(&out, testELF(62), p, testELF(62), "amd64", "fixture"); e == nil {
			t.Fatal("incompatible executable accepted")
		}
		if out.Len() != 0 {
			t.Fatal("partial output before validation")
		}
	}
}
func TestV15RejectsDynamicLoaderDependency(t *testing.T) {
	b := testELF(62)
	binary.LittleEndian.PutUint64(b[32:], 64)
	binary.LittleEndian.PutUint16(b[54:], 56)
	binary.LittleEndian.PutUint16(b[56:], 1)
	binary.LittleEndian.PutUint32(b[64:], 3)
	var out bytes.Buffer
	if e := Pack(&out, b, testELF(62), testELF(62), "amd64", "fixture"); e == nil {
		t.Fatal("PT_INTERP accepted")
	}
}
func TestV15ProfileRejectsUntrustedFields(t *testing.T) {
	p := testProfile(t)
	for _, u := range []string{"http://host", "https://user:pass@host", "https://host/path", "https://host/?a=x", "https://host/#x", "https://host:99999"} {
		q := *p
		q.ControllerURL = u
		if e := q.Validate(); e == nil {
			t.Fatal("unsafe origin accepted", u)
		}
	}
	q := *p
	q.CACertPEM += "-----BEGIN PRIVATE KEY-----\nAAAA\n-----END PRIVATE KEY-----\n"
	if e := q.Validate(); e == nil {
		t.Fatal("private key accepted")
	}
	q = *p
	q.EnrollmentCode = ""
	if e := q.Validate(); e == nil {
		t.Fatal("empty code")
	}
}

type shortWriter struct{}

func (shortWriter) Write(b []byte) (int, error) { return len(b) - 1, nil }
func TestV15ShortWritesNeverReportSuccess(t *testing.T) {
	raw := testTemplate(t)
	b, _ := Open(bytes.NewReader(raw), int64(len(raw)))
	if _, e := b.Write(shortWriter{}, nil); e == nil {
		t.Fatal("short write accepted")
	}
	if e := writeFull(shortWriter{}, []byte("test")); e != io.ErrShortWrite {
		t.Fatal(e)
	}
}
func TestV15ProfileIsOnlySecretSection(t *testing.T) {
	raw := testTemplate(t)
	p := testProfile(t)
	b, _ := Open(bytes.NewReader(raw), int64(len(raw)))
	var out bytes.Buffer
	b.Write(&out, p)
	if bytes.Contains(raw, []byte(p.EnrollmentCode)) {
		t.Fatal("reusable template secret")
	}
	var m map[string]any
	if e := json.Unmarshal(out.Bytes()[b.PayloadSize:out.Len()-footerSize], &m); e != nil {
		t.Fatal(e)
	}
	if m["profile"] == nil {
		t.Fatal("missing bound profile")
	}
}
func FuzzV15Bundle(f *testing.F) {
	f.Add([]byte("invalid"))
	var b bytes.Buffer
	Pack(&b, testELF(62), testELF(62), testELF(62), "amd64", "fixture")
	f.Add(b.Bytes())
	f.Fuzz(func(t *testing.T, b []byte) {
		if len(b) > 1<<20 {
			return
		}
		_, _ = Open(bytes.NewReader(b), int64(len(b)))
	})
}
