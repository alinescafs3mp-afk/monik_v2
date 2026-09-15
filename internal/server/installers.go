package server

import (
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/installerbundle"
	"github.com/alinescafs3mp-afk/monik_v2/internal/netutil"
	"github.com/alinescafs3mp-afk/monik_v2/internal/storage"
	"github.com/alinescafs3mp-afk/monik_v2/internal/version"
)

func (a *App) installerTemplate(platform string) (*os.File, *installerbundle.Bundle, error) {
	if platform != "linux-amd64" && platform != "linux-arm64" {
		return nil, nil, fmt.Errorf("single-file installer supports Linux amd64/arm64 with systemd")
	}
	dataRoot, e := os.OpenRoot(a.Cfg.DataDir)
	if e != nil {
		return nil, nil, fmt.Errorf("controller data directory is unavailable")
	}
	defer dataRoot.Close()
	dirInfo, e := dataRoot.Lstat("installer-templates")
	if e != nil || !dirInfo.IsDir() || dirInfo.Mode().Perm()&0022 != 0 {
		return nil, nil, fmt.Errorf("installer templates are not deployed in a protected directory; operator must run make installer-templates and deploy its outputs")
	}
	root, e := dataRoot.OpenRoot("installer-templates")
	if e != nil {
		return nil, nil, fmt.Errorf("installer template directory cannot be opened safely")
	}
	defer root.Close()
	actualDir, e := root.Stat(".")
	if e != nil || !os.SameFile(dirInfo, actualDir) {
		return nil, nil, fmt.Errorf("installer template directory changed")
	}
	name := platform + ".bin"
	info, e := root.Lstat(name)
	if e != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 {
		return nil, nil, fmt.Errorf("protected installer template is missing or invalid")
	}
	f, e := root.Open(name)
	if e != nil {
		return nil, nil, e
	}
	actual, e := f.Stat()
	if e != nil || !os.SameFile(info, actual) {
		f.Close()
		return nil, nil, fmt.Errorf("installer template changed")
	}
	b, e := installerbundle.Open(f, actual.Size())
	if e != nil {
		f.Close()
		return nil, nil, e
	}
	if b.Manifest.Profile != nil || b.Manifest.OS+"-"+b.Manifest.Arch != platform {
		f.Close()
		return nil, nil, fmt.Errorf("template is personalized or has a different platform")
	}
	// A template is part of this deployment, never an old arbitrary executable.
	expected := version.Commit
	got := b.Manifest.Build
	if expected != got && (len(expected) < 7 || len(got) < 7 || !(strings.HasPrefix(expected, got) || strings.HasPrefix(got, expected))) {
		f.Close()
		return nil, nil, fmt.Errorf("installer template and running controller were built from different commits")
	}
	return f, b, nil
}
func (a *App) handleInstallers(w http.ResponseWriter, r *http.Request, s *storage.Session) {
	if s.Role != "owner" {
		a.writeErr(w, 403, "forbidden", "owner role required")
		return
	}
	result := make([]map[string]any, 0, 2)
	for _, platform := range []string{"linux-amd64", "linux-arm64"} {
		f, b, e := a.installerTemplate(platform)
		row := map[string]any{"platform": platform, "ready": e == nil}
		if e != nil {
			row["reason"] = e.Error()
		} else {
			row["build"] = b.Manifest.Build
			row["bytes"] = b.PayloadSize
			f.Close()
		}
		result = append(result, row)
	}
	a.writeJSON(w, 200, map[string]any{"installers": result, "profile_lifetime_seconds": 3600, "one_machine_per_file": true})
}
func (a *App) handleInstallerDownload(w http.ResponseWriter, r *http.Request, s *storage.Session) {
	if s.Role != "owner" {
		a.writeErr(w, 403, "forbidden", "owner role required")
		return
	}
	if !a.requireRecent(w, s) {
		return
	}
	if r.TLS == nil || a.Cfg.RestoreMode {
		a.writeErr(w, 409, "installer_unavailable", "direct HTTPS and an active non-restored controller are required")
		return
	}
	if !a.rateLimit("installer:"+s.UserID, 6, time.Minute) {
		a.writeErr(w, 429, "rate_limited", "too many installer downloads")
		return
	}
	if !a.installerMu.TryLock() {
		a.writeErr(w, 429, "installer_busy", "another installer is being prepared; retry after it completes")
		return
	}
	defer a.installerMu.Unlock()
	var body struct {
		ControllerURL      string `json:"controller_url"`
		EnableAgentConsole bool   `json:"enable_agent_console"`
	}
	if e := parseJSONStrictLimit(r, &body, 4096); e != nil {
		a.writeErr(w, 400, "malformed", "invalid installer request")
		return
	}
	u, e := netutil.ValidateControllerURL(body.ControllerURL)
	if e != nil {
		a.writeErr(w, 400, "invalid_url", "a valid HTTPS controller origin is required")
		return
	}
	if a.TLS == nil {
		a.writeErr(w, 409, "tls_missing", "controller TLS is not configured")
		return
	}
	cert, e := a.TLS.LeafCertificate()
	if e != nil || cert.VerifyHostname(u.Hostname()) != nil || !a.Clock.Now().Before(cert.NotAfter) || a.Clock.Now().Before(cert.NotBefore) {
		a.writeErr(w, 409, "tls_mismatch", "loaded server certificate does not match the chosen address; preserve the CA and extend SAN before distributing an installer")
		return
	}
	platform := r.PathValue("platform")
	f, b, e := a.installerTemplate(platform)
	if e != nil {
		a.writeErr(w, 409, "installer_unavailable", e.Error())
		return
	}
	defer f.Close()
	p := &installerbundle.Profile{EnableAgentConsole: body.EnableAgentConsole, ControllerURL: strings.TrimRight(body.ControllerURL, "/"), ControllerID: a.ControllerID(), CACertPEM: string(a.CACertPEM()), EnrollmentCode: strings.Repeat("0", 32), ExpiresAt: a.Clock.Now().Add(time.Hour)}
	if e = p.Validate(); e != nil {
		a.writeErr(w, 409, "invalid_profile", e.Error())
		return
	}
	s = a.currentOwner(w, s, true)
	if s == nil {
		return
	}
	code, expires, e := a.Store.CreateInstallerCode(s.Username, b.Manifest.Build, platform)
	if e != nil {
		a.writeErr(w, 409, "enrollment_unavailable", e.Error())
		return
	}
	if _, e = a.Store.DB.ExecContext(r.Context(), `INSERT INTO audit_events(at,actor,action,entity,detail) VALUES(?,?,?,?,?)`, a.Clock.Now().UTC().Format(time.RFC3339Nano), s.Username, "installer.console_consent", platform, fmt.Sprintf("local_agent_console=%t; no controller secrets or reusable permits in audit", body.EnableAgentConsole)); e != nil {
		a.writeErr(w, 503, "audit_unavailable", "installer consent was not confirmed; unused enrollment expires")
		return
	}
	s = a.currentOwner(w, s, true)
	if s == nil {
		return
	}
	p.EnrollmentCode = code
	p.ExpiresAt = expires
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="monik-agent"`)
	w.Header().Set("Content-Length", strconv.FormatInt(b.SizeWith(p), 10))
	w.Header().Set("X-Monik-Profile-Expires", expires.UTC().Format(time.RFC3339))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if _, e = b.Write(w, p); e != nil {
		a.Log.Warn("installer download interrupted; unused code will expire", "platform", platform)
	}
}

// This endpoint is read-only and scoped to the authenticated agent. It proves
// committed live telemetry, not merely an enrolled DB row or a TCP listener.
func (a *App) handleInstallationStatus(w http.ResponseWriter, r *http.Request) {
	ag, _, ok := a.agentFromCurrent(r)
	if !ok {
		a.writeErr(w, 401, "unauthenticated", "current agent credential required")
		return
	}
	now := a.Clock.Now()
	ready := !a.Cfg.RestoreMode && !ag.Archived && !ag.Conflict && ag.ManagedReady && ag.SessionID != "" && ag.LastSeq > 0 && ag.AppliedRevision > 0 && ag.AppliedRevision == ag.DesiredRevision && ag.AppliedHash != "" && ag.AppliedHash == ag.DesiredHash && ag.LastLiveAt != nil
	if ready {
		age := now.Sub(*ag.LastLiveAt)
		ready = age >= 0 && age <= 20*time.Second
	}
	w.Header().Set("Cache-Control", "no-store")
	a.writeJSON(w, 200, map[string]any{"ready": ready, "agent_id": ag.ID, "controller_id": a.ControllerID(), "session_id": ag.SessionID, "seq": ag.LastSeq, "worker_digest": ag.WorkerDigest, "last_live_at": ag.LastLiveAt})
}
