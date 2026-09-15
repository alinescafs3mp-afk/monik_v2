package server

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/installerbundle"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"github.com/alinescafs3mp-afk/monik_v2/internal/secure"
	"github.com/alinescafs3mp-afk/monik_v2/internal/version"
)

// Synthetic ELF is solely a format fixture, never executed as an agent.
func v15ELF() []byte {
	b := make([]byte, 128)
	copy(b, []byte{0x7f, 'E', 'L', 'F', 2, 1, 1})
	binary.LittleEndian.PutUint16(b[16:], 2)
	binary.LittleEndian.PutUint16(b[18:], 62)
	binary.LittleEndian.PutUint32(b[20:], 1)
	binary.LittleEndian.PutUint16(b[52:], 64)
	binary.LittleEndian.PutUint16(b[54:], 56)
	binary.LittleEndian.PutUint16(b[58:], 64)
	return b
}
func v15Template(t *testing.T, a *App, build string) []byte {
	t.Helper()
	var b bytes.Buffer
	e := installerbundle.Pack(&b, v15ELF(), v15ELF(), v15ELF(), "amd64", build)
	if e != nil {
		t.Fatal(e)
	}
	d := filepath.Join(a.Cfg.DataDir, "installer-templates")
	if e = os.MkdirAll(d, 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(d, "linux-amd64.bin"), b.Bytes(), 0600); e != nil {
		t.Fatal(e)
	}
	return b.Bytes()
}
func TestV15InstallerDownloadIsScopedSecretAndOneUse(t *testing.T) {
	a, h := testApp(t)
	original := v15Template(t, a, version.Commit)
	call := audit6HTTP(t, a, h, time.Minute)
	w := call("POST", "/api/v1/installers/linux-amd64", map[string]any{"controller_url": a.Cfg.AdvertisedURL + "/"})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("cacheable enrollment")
	}
	if w.Header().Get("Content-Length") != strconv.Itoa(w.Body.Len()) {
		t.Fatal("wrong byte length")
	}
	b, e := installerbundle.Open(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if e != nil {
		t.Fatal(e)
	}
	p := b.Manifest.Profile
	if p == nil || p.ControllerURL != a.Cfg.AdvertisedURL || p.ControllerID != a.ControllerID() || strings.Contains(p.CACertPEM, "PRIVATE KEY") {
		t.Fatal("incorrect profile")
	}
	if p.ExpiresAt.Before(a.Clock.Now().Add(59*time.Minute)) || p.ExpiresAt.After(a.Clock.Now().Add(61*time.Minute)) {
		t.Fatal("wrong lifetime")
	}
	disk, e := os.ReadFile(filepath.Join(a.Cfg.DataDir, "installer-templates", "linux-amd64.bin"))
	if e != nil || !bytes.Equal(disk, original) {
		t.Fatal("personalization mutated shared template")
	}
	var detail string
	if e = a.Store.DB.QueryRow(`SELECT detail FROM audit_events WHERE action='installer.download'`).Scan(&detail); e != nil || strings.Contains(detail, p.EnrollmentCode) {
		t.Fatal("missing audit or leaked credential", e)
	}
	enroll := func(id string) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(protocol.EnrollRequest{Code: p.EnrollmentCode, AgentID: id, Credential: strings.Repeat("a", 64), Hostname: id, OS: "linux", Arch: "amd64"})
		r := httptest.NewRequest("POST", "https://localhost/api/v1/agent/enroll", bytes.NewReader(raw))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w = enroll("first-host"); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = enroll("second-host"); w.Code == 200 {
		t.Fatal("one-machine file admitted a second machine")
	}
}
func TestV15InstallerRejectsUnavailableTLSExpiredAuthAndAnonymous(t *testing.T) {
	a, h := testApp(t)
	call := audit6HTTP(t, a, h, time.Minute)
	w := call("POST", "/api/v1/installers/linux-amd64", map[string]any{"controller_url": a.Cfg.AdvertisedURL})
	if w.Code != 409 {
		t.Fatal(w.Code)
	}
	v15Template(t, a, version.Commit)
	w = call("POST", "/api/v1/installers/linux-amd64", map[string]any{"controller_url": "https://untrusted.example:8777"})
	if w.Code != 409 {
		t.Fatal(w.Code)
	}
	stale := audit6HTTP(t, a, h, -time.Minute)
	if w = stale("POST", "/api/v1/installers/linux-amd64", map[string]any{"controller_url": a.Cfg.AdvertisedURL}); w.Code != 401 {
		t.Fatal(w.Code)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "https://localhost/api/v1/installers", nil))
	if w.Code != 401 {
		t.Fatal(w.Code)
	}
	var n int
	if e := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM enrollment_codes`).Scan(&n); e != nil || n != 0 {
		t.Fatal("rejected request minted enrollment", n, e)
	}
}
func TestV15InstallerRejectsTemplateFromDifferentBuild(t *testing.T) {
	a, _ := testApp(t)
	v15Template(t, a, strings.Repeat("f", 40))
	if f, _, e := a.installerTemplate("linux-amd64"); e == nil {
		f.Close()
		t.Fatal("mismatched template")
	}
}
func TestV15InstallationStatusRequiresLiveCurrentManagedReport(t *testing.T) {
	a, h := testApp(t)
	v14Register(t, a, "host")
	ag, e := a.Store.Agent("host")
	if e != nil {
		t.Fatal(e)
	}
	if e = a.Store.SetDesired("host", ag.DesiredRevision, ag.DesiredHash, ag.DesiredConfig); e != nil {
		t.Fatal(e)
	}
	read := func(id, cred string) (int, bool) {
		t.Helper()
		r := httptest.NewRequest("GET", "https://localhost/api/v1/agent/installation", nil)
		r.Header.Set("X-Monik-Agent-Id", id)
		r.Header.Set("Authorization", "Bearer "+cred)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		var out struct {
			Ready bool `json:"ready"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out.Ready
	}
	if code, ready := read("host", "fixture"); code != 200 || ready {
		t.Fatal(code, ready)
	}
	now := a.Clock.Now()
	rep := protocol.AgentReport{SchemaVersion: protocol.SchemaVersion, AgentID: "host", SessionID: "s1", Sequence: 1, ObservedAt: now, IsLive: true, ManagedReady: true, WorkerDigest: secure.SHA256Bytes([]byte("worker")), ConfigRevision: ag.DesiredRevision, ConfigHash: ag.DesiredHash}
	if _, e = a.Store.AcceptReport(rep); e != nil {
		t.Fatal(e)
	}
	if code, ready := read("host", "fixture"); code != 200 || !ready {
		t.Fatal(code, ready)
	}
	if code, _ := read("other-host", "fixture"); code != 401 {
		t.Fatal("cross-agent status", code)
	}
	if _, e = a.Store.DB.Exec(`UPDATE agents SET last_live_at=? WHERE id='host'`, now.Add(-time.Minute).UTC().Format(time.RFC3339Nano)); e != nil {
		t.Fatal(e)
	}
	if _, ready := read("host", "fixture"); ready {
		t.Fatal("stale report is ready")
	}
}

func TestV15InstallerTemplateCannotComeFromEscapingDirectory(t *testing.T) {
	a, _ := testApp(t)
	external := t.TempDir()
	if e := os.Symlink(external, filepath.Join(a.Cfg.DataDir, "installer-templates")); e != nil {
		t.Skip("symlink not available", e)
	}
	if f, _, e := a.installerTemplate("linux-amd64"); e == nil {
		f.Close()
		t.Fatal("linked template directory accepted")
	}
}
