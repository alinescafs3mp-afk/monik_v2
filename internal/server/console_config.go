package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/jsonutil"
	"github.com/alinescafs3mp-afk/monik_v2/internal/secure"
	"github.com/alinescafs3mp-afk/monik_v2/internal/storage"
)

const consoleConfigLimit = 64 << 10

// encoding/json matches struct field names case-insensitively. Reject alternate
// spellings too, so "Host" cannot silently override the visible "host" field.
func exactConsoleFields(b []byte, names ...string) error {
	if err := jsonutil.Validate(b); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(b, &fields) != nil || fields == nil {
		return fmt.Errorf("object required")
	}
	allowed := map[string]bool{}
	for _, n := range names {
		allowed[n] = true
	}
	for key := range fields {
		if !allowed[key] {
			return fmt.Errorf("unknown or noncanonical field")
		}
	}
	return nil
}
func exactConsoleTarget(b []byte) error {
	return exactConsoleFields(b, "host", "port", "username", "host_key_sha256", "host_key_type")
}

type consoleConfiguration struct {
	Targets map[string]consoleTarget `json:"targets"`
}

func consoleConfigRevision(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// The config is still a fixed, protected local file. The owner editor changes
// one approved destination, never credentials and never an arbitrary path.
// Callers serialise mutations; reads see either complete atomic snapshot.
func (a *App) readConsoleConfiguration() (consoleConfiguration, string, error) {
	empty := consoleConfiguration{Targets: map[string]consoleTarget{}}
	dir, err := os.Stat(a.Cfg.DataDir)
	if err != nil || !dir.IsDir() || dir.Mode().Perm()&0022 != 0 {
		return empty, "", fmt.Errorf("Каталог контроллера недоступен или разрешает запись группе/остальным; исправьте права локально.")
	}
	path := filepath.Join(a.Cfg.DataDir, "console-targets.json")
	before, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return empty, consoleConfigRevision([]byte("absent-console-config-v1")), nil
	}
	if err != nil || !before.Mode().IsRegular() || before.Mode().Perm()&0022 != 0 || before.Size() > consoleConfigLimit {
		return empty, "", fmt.Errorf("Некорректный console-targets.json: нужен обычный защищённый файл до 64 КиБ, не ссылка.")
	}
	f, err := os.Open(path)
	if err != nil {
		return empty, "", fmt.Errorf("Не удалось открыть console-targets.json.")
	}
	defer f.Close()
	after, err := f.Stat()
	if err != nil || !os.SameFile(before, after) {
		return empty, "", fmt.Errorf("Файл SSH-настроек изменился при чтении.")
	}
	b, err := io.ReadAll(io.LimitReader(f, consoleConfigLimit+1))
	if err != nil || len(b) > consoleConfigLimit || exactConsoleFields(b, "targets") != nil {
		return empty, "", fmt.Errorf("Некорректный или неоднозначный JSON в console-targets.json; восстановите файл локально.")
	}
	var raw struct {
		Targets map[string]json.RawMessage `json:"targets"`
	}
	if json.Unmarshal(b, &raw) != nil {
		return empty, "", fmt.Errorf("Некорректная SSH-конфигурация.")
	}
	for _, v := range raw.Targets {
		if exactConsoleTarget(v) != nil {
			return empty, "", fmt.Errorf("Неоднозначные или неизвестные поля SSH-цели.")
		}
	}
	var cfg consoleConfiguration
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(&cfg) != nil || cfg.Targets == nil || len(cfg.Targets) > 256 {
		return empty, "", fmt.Errorf("Некорректная структура console-targets.json.")
	}
	for id, t := range cfg.Targets {
		if id == "" || len(id) > 128 || validateConsoleTarget(t) != nil {
			return empty, "", fmt.Errorf("Некорректная SSH-цель в console-targets.json; проверьте IP, порт, логин и SHA256-отпечаток.")
		}
	}
	return cfg, consoleConfigRevision(b), nil
}

type consoleConfigureRequest struct {
	BaseRevision        string         `json:"base_revision"`
	Enabled             *bool          `json:"enabled"`
	Target              *consoleTarget `json:"target,omitempty"`
	FingerprintVerified bool           `json:"fingerprint_verified"`
}

func (a *App) handleConsoleConfigure(w http.ResponseWriter, r *http.Request, s *storage.Session) {
	if s.Role != "owner" {
		a.writeErr(w, 403, "forbidden", "owner role required")
		return
	}
	if !a.requireRecent(w, s) {
		return
	}
	if r.TLS == nil {
		a.writeErr(w, 400, "tls_required", "SSH configuration requires HTTPS")
		return
	}
	if a.Cfg.RestoreMode {
		a.writeErr(w, 409, "restore_mode", "configuration disabled during restore reconciliation")
		return
	}
	if !a.rateLimit("console-config:"+s.UserID, 20, time.Minute) {
		a.writeErr(w, 429, "rate_limited", "too many SSH configuration changes")
		return
	}
	id := r.PathValue("id")
	ag, err := a.Store.Agent(id)
	if err != nil {
		a.writeErr(w, 404, "not_found", "machine not found")
		return
	}
	if ag.Revoked || ag.Archived {
		a.writeErr(w, 409, "access_revoked", "machine access revoked or archived")
		return
	}
	b, err := io.ReadAll(io.LimitReader(r.Body, 8193))
	if err != nil || len(b) > 8192 || exactConsoleFields(b, "base_revision", "enabled", "target", "fingerprint_verified") != nil {
		a.writeErr(w, 400, "invalid_request", "one bounded unambiguous JSON request required")
		return
	}
	var rawReq struct {
		Target json.RawMessage `json:"target"`
	}
	if json.Unmarshal(b, &rawReq) != nil {
		a.writeErr(w, 400, "invalid_request", "request object required")
		return
	}
	if len(rawReq.Target) > 0 && exactConsoleTarget(rawReq.Target) != nil {
		a.writeErr(w, 400, "invalid_target", "canonical SSH target fields required")
		return
	}
	var req consoleConfigureRequest
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(&req) != nil || len(req.BaseRevision) != 64 || req.Enabled == nil {
		a.writeErr(w, 400, "invalid_request", "base_revision and enabled are required")
		return
	}
	if *req.Enabled {
		if req.Target == nil || !req.FingerprintVerified {
			a.writeErr(w, 400, "trust_confirmation", "Independently verify the SSH host-key fingerprint and explicitly confirm it.")
			return
		}
		req.Target.Host = strings.TrimSpace(req.Target.Host)
		req.Target.Username = strings.TrimSpace(req.Target.Username)
		req.Target.Fingerprint = strings.TrimSpace(req.Target.Fingerprint)
		if e := validateConsoleTarget(*req.Target); e != nil {
			a.writeErr(w, 400, "invalid_target", e.Error())
			return
		}
	} else if req.Target != nil {
		a.writeErr(w, 400, "invalid_request", "disabled configuration must omit target")
		return
	}
	a.consoleConfigMu.Lock()
	defer a.consoleConfigMu.Unlock()
	s = a.currentOwner(w, s, true)
	if s == nil {
		return
	}
	currentAgent, err := a.Store.Agent(id)
	if err != nil || currentAgent.Revoked || currentAgent.Archived {
		a.writeErr(w, 409, "access_revoked", "machine access changed while reading request")
		return
	}
	cfg, revision, err := a.readConsoleConfiguration()
	if err != nil {
		a.writeErr(w, 409, "config_invalid", err.Error())
		return
	}
	if revision != req.BaseRevision {
		a.writeErr(w, 409, "config_conflict", "SSH-настройки изменены. Перечитайте их перед сохранением.")
		return
	}
	current, exists := cfg.Targets[id]
	changed := (!*req.Enabled && exists) || (*req.Enabled && (!exists || targetIdentity(current) != targetIdentity(*req.Target)))
	if !changed {
		a.writeJSON(w, 200, map[string]any{"saved": true, "config_revision": revision, "enabled": *req.Enabled})
		return
	}
	if *req.Enabled {
		cfg.Targets[id] = *req.Target
	} else {
		delete(cfg.Targets, id)
	}
	body, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil || len(body) > consoleConfigLimit || len(cfg.Targets) > 256 {
		a.writeErr(w, 400, "config_limit", "SSH configuration size limit reached")
		return
	}
	body = append(body, '\n')
	if len(body) > consoleConfigLimit {
		a.writeErr(w, 400, "config_limit", "SSH configuration size limit reached")
		return
	}
	// Audit intent must be durable before publishing authority. It records no
	// password, private key, input or terminal output. File and SQLite do not
	// share a transaction; post-write audit failure is an UNKNOWN result.
	detail := fmt.Sprintf("enabled=%t; previous_revision=%s; new_revision=%s", *req.Enabled, revision, consoleConfigRevision(body))
	if _, err = a.Store.DB.ExecContext(r.Context(), `INSERT INTO audit_events(at,actor,action,entity,detail) VALUES(?,?,?,?,?)`, a.Clock.Now().UTC().Format(time.RFC3339Nano), s.Username, "console.configure.requested", id, detail); err != nil {
		a.writeErr(w, 503, "audit_unavailable", "Изменение не отправлено: журнал действий недоступен.")
		return
	}
	s = a.currentOwner(w, s, true)
	if s == nil {
		return
	}
	currentAgent, err = a.Store.Agent(id)
	if err != nil || currentAgent.Revoked || currentAgent.Archived {
		a.writeErr(w, 409, "access_revoked", "machine access changed while saving configuration")
		return
	}
	if err = secure.AtomicWrite(filepath.Join(a.Cfg.DataDir, "console-targets.json"), body, 0600); err != nil {
		a.writeErr(w, 503, "outcome_unknown", "Не удалось подтвердить запись SSH-настроек. Перечитайте текущее состояние; автоматического повтора нет.")
		return
	}
	// Invalidate not-yet-used tickets and stop a live old session. A concurrent
	// handshake additionally rechecks the target at the host-key callback.
	a.console.mu.Lock()
	for token, t := range a.console.tickets {
		if t.Agent == id {
			delete(a.console.tickets, token)
		}
	}
	stop := a.console.active[id]
	a.console.mu.Unlock()
	if stop != nil {
		stop()
	}
	if _, err = a.Store.DB.ExecContext(r.Context(), `INSERT INTO audit_events(at,actor,action,entity,detail) VALUES(?,?,?,?,?)`, a.Clock.Now().UTC().Format(time.RFC3339Nano), s.Username, "console.configure.saved", id, detail); err != nil {
		a.writeErr(w, 503, "outcome_unknown", "Файл записан, подтверждение в журнале не сохранено. Перечитайте SSH-настройки.")
		return
	}
	a.writeJSON(w, 200, map[string]any{"saved": true, "config_revision": consoleConfigRevision(body), "enabled": *req.Enabled})
}
