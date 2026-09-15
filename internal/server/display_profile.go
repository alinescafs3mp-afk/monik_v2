package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/storage"
)

func (a *App) handleTVProfile(w http.ResponseWriter, r *http.Request, s *storage.Session) {
	w.Header().Set("Cache-Control", "no-store")
	p, e := a.Store.TVProfile()
	if e != nil {
		a.writeErr(w, 503, "display_unavailable", "Общий ТВ-профиль недоступен. Настройки не сброшены.")
		return
	}
	a.writeJSON(w, 200, map[string]any{"controller_id": a.ControllerID(), "profile": p, "server_time": a.Clock.Now()})
}
func (a *App) handleTVProfileSave(w http.ResponseWriter, r *http.Request, s *storage.Session) {
	w.Header().Set("Cache-Control", "no-store")
	if s.Role != "owner" {
		a.writeErr(w, 403, "forbidden", "Изменять общий ТВ-профиль может только владелец.")
		return
	}
	// No recent-auth escalation: these fields grant no authority. CSRF is checked
	// by needAuth, with an additional same-origin check when browsers supply it.
	if origin := r.Header.Get("Origin"); origin != "" && (!consoleOrigin(r)) {
		a.writeErr(w, 403, "origin", "Недопустимый источник запроса.")
		return
	}
	if !a.rateLimit("display-save:"+s.UserID, 60, time.Minute) {
		a.writeErr(w, 429, "rate_limited", "Слишком частое сохранение. Подождите перед новой правкой.")
		return
	}
	var raw map[string]json.RawMessage
	if e := parseJSONLimit(r, &raw, 4096); e != nil || len(raw) != 3 {
		a.writeErr(w, 400, "invalid_display", "Некорректный ТВ-профиль.")
		return
	}
	var body struct {
		Revision  *int64
		RequestID string
		Value     storage.TVLayout
	}
	if e := json.Unmarshal(raw["expected_revision"], &body.Revision); e != nil || body.Revision == nil || *body.Revision < 0 || *body.Revision >= 9007199254740990 {
		a.writeErr(w, 400, "invalid_display", "Требуется ревизия показанного профиля.")
		return
	}
	if e := json.Unmarshal(raw["request_id"], &body.RequestID); e != nil || len(body.RequestID) != 36 {
		a.writeErr(w, 400, "invalid_display", "Требуется идентификатор сохранения.")
		return
	}
	if e := json.Unmarshal(raw["value"], &body.Value); e != nil || body.Value.Validate() != nil {
		a.writeErr(w, 400, "invalid_display", "Недопустимые размеры или плотность ТВ.")
		return
	}
	// Validate request identity before a write; never accept unbounded/log-injected IDs.
	for i, c := range body.RequestID {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				a.writeErr(w, 400, "invalid_display", "Некорректный идентификатор.")
				return
			}
		} else if !(c >= 'a' && c <= 'f' || c >= '0' && c <= '9') {
			a.writeErr(w, 400, "invalid_display", "Некорректный идентификатор.")
			return
		}
	}
	a.controlMu.Lock()
	defer a.controlMu.Unlock()
	current, err := a.Store.RevalidateSession(s)
	if err != nil || current.Role != "owner" {
		a.writeErr(w, 401, "unauthorized", "session or permissions changed; reload before editing")
		return
	}
	p, e := a.Store.SaveTVProfile(*body.Revision, body.RequestID, s.Username, body.Value)
	if errors.Is(e, storage.ErrConflict) || errors.Is(e, storage.ErrIdempotencyConflict) {
		a.writeErr(w, 409, "display_conflict", "Общий ТВ-профиль изменён с другого устройства. Перечитайте его перед новой правкой.")
		return
	}
	if e != nil {
		a.writeErr(w, 503, "display_save_unknown", "Сохранение ТВ-профиля не подтверждено. Перечитайте профиль, повтор автоматически не выполняется.")
		return
	}
	a.writeJSON(w, 200, map[string]any{"saved": true, "controller_id": a.ControllerID(), "profile": p, "server_time": a.Clock.Now()})
}
