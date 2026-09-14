package server

import (
	"encoding/hex"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"github.com/alinescafs3mp-afk/monik_v2/internal/storage"
	"github.com/alinescafs3mp-afk/monik_v2/internal/tufutil"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

func (a *App) handleAnnounce(w http.ResponseWriter, r *http.Request) {
	if !a.SetupComplete() {
		a.writeErr(w, 409, "setup_required", "controller setup required")
		return
	}
	if !a.rateLimit("announce:global", 600, time.Minute) || !a.rateLimit("announce:"+clientIP(r), 300, time.Minute) {
		w.Header().Set("Retry-After", "60")
		a.writeErr(w, 429, "rate_limited", "announcement rate exceeded")
		return
	}
	var req protocol.Announcement
	if err := parseJSONStrictLimit(r, &req, 4096); err != nil {
		a.writeErr(w, 400, "malformed", "invalid bounded announcement")
		return
	}
	b, err := hex.DecodeString(req.Credential)
	if err != nil || len(b) != 32 || req.AgentID == "" || len(req.AgentID) > 128 || req.Hostname == "" {
		a.writeErr(w, 400, "invalid_identity", "identity and persisted random proof required")
		return
	}
	for _, v := range []string{req.AgentID, req.Hostname, req.DisplayName, req.OS, req.Arch, req.Version} {
		if len(v) > 255 || strings.IndexFunc(v, func(c rune) bool { return c < 32 || c == 127 }) >= 0 {
			a.writeErr(w, 400, "invalid_metadata", "metadata too long or contains controls")
			return
		}
	}
	state, created, err := a.Store.Announce(req, clientIP(r))
	if err != nil {
		a.writeErr(w, 409, "registration_conflict", "identity unavailable or pending queue capacity reached; no enrollment performed")
		return
	}
	out := protocol.AnnouncementResponse{State: state, Fingerprint: protocol.RegistrationFingerprint(req.AgentID, req.Credential), RetryAfterSeconds: 30}
	if created {
		_ = a.Store.AppendEvent("enrollment", "candidate", req.AgentID, 0, map[string]any{"state": "pending"})
	}
	if state == "approved" {
		ag, err := a.Store.Agent(req.AgentID)
		if err != nil {
			a.writeErr(w, 500, "db", "registration lookup failed")
			return
		}
		root := ""
		if b, e := tufutil.LoadTrustedRoot(filepath.Join(a.Cfg.DataDir, "tuf")); e == nil {
			root = string(b)
		}
		out.Enrollment = &protocol.EnrollResponse{AgentID: req.AgentID, Credential: req.Credential, ControllerID: a.ControllerID(), AdvertisedURL: a.Cfg.AdvertisedURL, CACertPEM: string(a.CACertPEM()), EndpointGeneration: ag.EndpointGeneration, ConfigRevision: ag.DesiredRevision, UpdateRootJSON: root}
	}
	a.writeJSON(w, 200, out)
}
func (a *App) handlePendingAgents(w http.ResponseWriter, r *http.Request, s *storage.Session) {
	if s.Role != "owner" {
		a.writeErr(w, 403, "forbidden", "owner only")
		return
	}
	candidates, err := a.Store.AgentCandidates()
	if err != nil {
		a.writeErr(w, 500, "db", "pending registration lookup failed")
		return
	}
	a.writeJSON(w, 200, map[string]any{"candidates": candidates, "server_time": a.Clock.Now()})
}
