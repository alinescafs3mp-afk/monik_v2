package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/storage"
)

func (a *App) handleRollouts(w http.ResponseWriter, r *http.Request, s *storage.Session) {
	if id := r.URL.Query().Get("operation_id"); id != "" {
		result, err := a.Store.Rollout(id)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				a.writeErr(w, 404, "rollout_not_found", "No batched rollout for this operation")
			} else {
				a.writeErr(w, 500, "rollout_unavailable", "Rollout state could not be read")
			}
			return
		}
		a.writeJSON(w, 200, result)
		return
	}
	rows, err := a.Store.Rollouts(30)
	if err != nil {
		a.writeErr(w, 500, "rollouts_unavailable", "could not load rollout progress")
		return
	}
	a.writeJSON(w, 200, map[string]any{"rollouts": rows, "server_time": a.Clock.Now().UTC()})
}
func (a *App) rolloutBackground(ctx context.Context) {
	ticker := a.Clock.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C():
			a.controlMu.Lock()
			if !a.Cfg.RestoreMode {
				if err := a.Store.AdvanceRollouts(); err != nil {
					a.Log.Error("rollout scheduling failed; jobs remain gated", "error", err)
				}
			}
			a.controlMu.Unlock()
		}
	}
}
