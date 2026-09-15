// Package installerbundle implements a bounded, self-contained Linux installer.
// A template contains the installer ELF, worker ELF and supervisor ELF. The
// controller adds a one-machine enrollment profile without running a compiler.
// Hashes detect corruption; authenticity comes from the trusted HTTPS download.
package installerbundle

import (
	"bytes"
	"crypto/sha256"
	"crypto/x509"
	"debug/elf"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/jsonutil"
)

const (
	MaxExecutable int64 = 64 << 20
	MaxManifest   int64 = 64 << 10
	MaxFile       int64 = 3*MaxExecutable + MaxManifest + footerSize
	footerSize          = 56
	Magic               = "MONIK-INSTALL-V1"
)

type Part struct {
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type Profile struct {
	ControllerURL  string    `json:"controller_url"`
	ControllerID   string    `json:"controller_id"`
	CACertPEM      string    `json:"ca_cert_pem"`
	EnrollmentCode string    `json:"enrollment_code"`
	ExpiresAt      time.Time `json:"expires_at"`
}

type Manifest struct {
	Format     int      `json:"format"`
	OS         string   `json:"os"`
	Arch       string   `json:"arch"`
	Build      string   `json:"build"`
	Installer  Part     `json:"installer"`
	Worker     Part     `json:"worker"`
	Supervisor Part     `json:"supervisor"`
	Profile    *Profile `json:"profile,omitempty"`
}

type Bundle struct {
	Reader      io.ReaderAt
	Manifest    Manifest
	PayloadSize int64
}

// Open validates lengths, complete unambiguous JSON, all three digests and ELF
// architectures before any caller can extract or execute included programs.
func Open(r io.ReaderAt, size int64) (*Bundle, error) {
	if size < footerSize || size > MaxFile {
		return nil, fmt.Errorf("installer file has invalid size")
	}
	tail := make([]byte, footerSize)
	if _, err := r.ReadAt(tail, size-footerSize); err != nil {
		return nil, fmt.Errorf("installer footer is incomplete")
	}
	if string(tail[:16]) != Magic {
		return nil, fmt.Errorf("this is an unprepared installer; download a prepared file from Add machine")
	}
	n := binary.BigEndian.Uint64(tail[16:24])
	if n == 0 || n > uint64(MaxManifest) || n > uint64(size-footerSize) {
		return nil, fmt.Errorf("invalid installer manifest length")
	}
	raw := make([]byte, int(n))
	if _, err := r.ReadAt(raw, size-footerSize-int64(n)); err != nil {
		return nil, fmt.Errorf("incomplete installer manifest")
	}
	sum := sha256.Sum256(raw)
	if !bytes.Equal(sum[:], tail[24:]) {
		return nil, fmt.Errorf("installer manifest checksum mismatch")
	}
	var m Manifest
	if err := jsonutil.Validate(raw); err != nil {
		return nil, fmt.Errorf("invalid installer manifest JSON")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&m); err != nil {
		return nil, fmt.Errorf("unsupported installer manifest fields")
	}
	if m.Format != 1 || m.OS != "linux" || (m.Arch != "amd64" && m.Arch != "arm64") || len(m.Build) < 1 || len(m.Build) > 128 {
		return nil, fmt.Errorf("unsupported installer format or platform")
	}
	payload := size - footerSize - int64(n)
	offset := int64(0)
	for _, p := range []Part{m.Installer, m.Worker, m.Supervisor} {
		if p.Size < 64 || p.Size > MaxExecutable || len(p.SHA256) != 64 || offset > payload-p.Size {
			return nil, fmt.Errorf("invalid executable bounds")
		}
		if err := verifyPart(r, offset, p, m.Arch); err != nil {
			return nil, err
		}
		offset += p.Size
	}
	if offset != payload {
		return nil, fmt.Errorf("unexpected bytes outside installer parts")
	}
	if m.Profile != nil {
		if err := m.Profile.Validate(); err != nil {
			return nil, err
		}
	}
	return &Bundle{Reader: r, Manifest: m, PayloadSize: payload}, nil
}

func verifyPart(r io.ReaderAt, off int64, p Part, arch string) error {
	h := sha256.New()
	if _, err := io.Copy(h, io.NewSectionReader(r, off, p.Size)); err != nil {
		return fmt.Errorf("cannot read installer executable")
	}
	if hex.EncodeToString(h.Sum(nil)) != p.SHA256 {
		return fmt.Errorf("installer executable checksum mismatch")
	}
	f, err := elf.NewFile(io.NewSectionReader(r, off, p.Size))
	if err != nil {
		return fmt.Errorf("installer includes an invalid ELF")
	}
	defer f.Close()
	want := elf.EM_X86_64
	if arch == "arm64" {
		want = elf.EM_AARCH64
	}
	if f.Machine != want || f.Class != elf.ELFCLASS64 || f.Data != elf.ELFDATA2LSB || (f.Type != elf.ET_EXEC && f.Type != elf.ET_DYN) {
		return fmt.Errorf("installer executable platform mismatch")
	}
	// Payloads must be statically linked: the remote machine need not have a
	// matching dynamic loader or libc version merely to enroll the agent.
	for _, p := range f.Progs {
		if p.Type == elf.PT_INTERP {
			return fmt.Errorf("installer executables must be statically linked")
		}
	}
	return nil
}

func (p Profile) Validate() error {
	u, err := url.Parse(p.ControllerURL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.ContainsAny(p.ControllerURL, "\r\n\t") {
		return fmt.Errorf("installer requires an HTTPS controller origin")
	}
	if port := u.Port(); port != "" {
		n, e := strconv.Atoi(port)
		if e != nil || n < 1 || n > 65535 {
			return fmt.Errorf("invalid controller port")
		}
	}
	if p.ControllerID == "" || len(p.ControllerID) > 128 || len(p.EnrollmentCode) < 12 || len(p.EnrollmentCode) > 128 || p.ExpiresAt.IsZero() {
		return fmt.Errorf("installer enrollment profile is incomplete")
	}
	if len(p.CACertPEM) == 0 || len(p.CACertPEM) > 32<<10 {
		return fmt.Errorf("invalid embedded CA size")
	}
	rest := []byte(p.CACertPEM)
	count := 0
	for len(bytes.TrimSpace(rest)) > 0 {
		block, next := pem.Decode(rest)
		if block == nil || block.Type != "CERTIFICATE" {
			return fmt.Errorf("installer profile must contain only public CA certificates")
		}
		cert, e := x509.ParseCertificate(block.Bytes)
		if e != nil || !cert.IsCA || !cert.BasicConstraintsValid {
			return fmt.Errorf("invalid embedded CA certificate")
		}
		count++
		rest = next
	}
	if count == 0 {
		return fmt.Errorf("no embedded CA certificate")
	}
	return nil
}

// Write copies the already verified exact executables and publishes one footer.
// A nil profile makes a reusable template; templates never contain credentials.
func (b *Bundle) Write(w io.Writer, profile *Profile) (int64, error) {
	m := b.Manifest
	m.Profile = profile
	if profile != nil {
		if err := profile.Validate(); err != nil {
			return 0, err
		}
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return 0, err
	}
	if int64(len(raw)) > MaxManifest {
		return 0, fmt.Errorf("installer manifest too large")
	}
	total := b.PayloadSize + int64(len(raw)) + footerSize
	if _, err = io.CopyN(w, io.NewSectionReader(b.Reader, 0, b.PayloadSize), b.PayloadSize); err != nil {
		return 0, err
	}
	if err = writeFull(w, raw); err != nil {
		return 0, err
	}
	tail := make([]byte, footerSize)
	copy(tail, Magic)
	binary.BigEndian.PutUint64(tail[16:24], uint64(len(raw)))
	sum := sha256.Sum256(raw)
	copy(tail[24:], sum[:])
	if err = writeFull(w, tail); err != nil {
		return 0, err
	}
	return total, nil
}
func (b *Bundle) SizeWith(profile *Profile) int64 {
	m := b.Manifest
	m.Profile = profile
	raw, _ := json.Marshal(m)
	return b.PayloadSize + int64(len(raw)) + footerSize
}
func (b *Bundle) CopyWorker(w io.Writer) error {
	_, e := io.CopyN(w, io.NewSectionReader(b.Reader, b.Manifest.Installer.Size, b.Manifest.Worker.Size), b.Manifest.Worker.Size)
	return e
}
func (b *Bundle) CopySupervisor(w io.Writer) error {
	_, e := io.CopyN(w, io.NewSectionReader(b.Reader, b.Manifest.Installer.Size+b.Manifest.Worker.Size, b.Manifest.Supervisor.Size), b.Manifest.Supervisor.Size)
	return e
}
func writeFull(w io.Writer, b []byte) error {
	n, e := w.Write(b)
	if e == nil && n != len(b) {
		e = io.ErrShortWrite
	}
	return e
}

// Pack makes a template from three already built executables of one platform.
func Pack(w io.Writer, installer, worker, supervisor []byte, arch, build string) error {
	m := Manifest{Format: 1, OS: "linux", Arch: arch, Build: build}
	parts := []struct {
		data []byte
		part *Part
	}{{installer, &m.Installer}, {worker, &m.Worker}, {supervisor, &m.Supervisor}}
	for _, p := range parts {
		sum := sha256.Sum256(p.data)
		*p.part = Part{Size: int64(len(p.data)), SHA256: hex.EncodeToString(sum[:])}
		if p.part.Size > MaxExecutable || p.part.Size < 64 {
			return fmt.Errorf("invalid executable size")
		}
		if e := verifyPart(bytes.NewReader(p.data), 0, *p.part, arch); e != nil {
			return e
		}
	}
	if (arch != "amd64" && arch != "arm64") || build == "" || len(build) > 128 {
		return fmt.Errorf("invalid template platform/build")
	}
	payload := io.MultiReader(bytes.NewReader(installer), bytes.NewReader(worker), bytes.NewReader(supervisor))
	if _, e := io.Copy(w, payload); e != nil {
		return e
	}
	raw, e := json.Marshal(m)
	if e != nil {
		return e
	}
	if e = writeFull(w, raw); e != nil {
		return e
	}
	tail := make([]byte, footerSize)
	copy(tail, Magic)
	binary.BigEndian.PutUint64(tail[16:24], uint64(len(raw)))
	sum := sha256.Sum256(raw)
	copy(tail[24:], sum[:])
	return writeFull(w, tail)
}
