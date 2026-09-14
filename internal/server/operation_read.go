package server

import (
	"errors"
	"github.com/alinescafs3mp-afk/monik_v2/internal/storage"
	"net/http"
)

func (a *App) handleOperationCounts(w http.ResponseWriter, r *http.Request, s *storage.Session) {
	counts, e := a.Store.OperationCounts()
	if e != nil {
		a.writeErr(w, 500, "db", "could not load operation counts")
		return
	}
	a.writeJSON(w, 200, counts)
}
func (a *App) handleOperationReads(w http.ResponseWriter, r *http.Request, s *storage.Session) {
	if s.Role != "owner" {
		a.writeErr(w, 403, "forbidden", "owner role required")
		return
	}
	var body struct {
		Read    *bool                         `json:"read"`
		Targets []storage.OperationReadTarget `json:"targets"`
	}
	if e := parseJSONStrictLimit(r, &body, 32<<10); e != nil || body.Read == nil {
		a.writeErr(w, 400, "malformed", "read flag and a versioned selection are required")
		return
	}
	// This is server-side attention metadata, not a remote execution request.
	e := a.Store.MarkOperationsRead(s.Username, *body.Read, body.Targets)
	switch {
	case errors.Is(e, storage.ErrInvalidOperationQuery):
		a.writeErr(w, 400, "selection", "select 1 to 100 unique operations with attention revisions")
		return
	case errors.Is(e, storage.ErrNotFound):
		a.writeErr(w, 404, "not_found", "operation not found; selection was not changed")
		return
	case errors.Is(e, storage.ErrConflict):
		a.writeErr(w, 409, "attention_conflict", "Результат выбранной операции изменился. Перечитайте его перед отметкой; новые ошибки не были скрыты.")
		return
	case e != nil:
		a.writeErr(w, 500, "db", "could not commit read state; reload operations before retrying")
		return
	}
	a.writeJSON(w, 200, map[string]any{"saved": true, "read": *body.Read, "count": len(body.Targets)})
}
