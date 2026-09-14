package server

import (
	"github.com/alinescafs3mp-afk/monik_v2/internal/secure"
	"github.com/alinescafs3mp-afk/monik_v2/internal/storage"
	"net/http"
	"time"
)

func (a *App) handlePasswordChange(w http.ResponseWriter, r *http.Request, s *storage.Session) {
	if s.Role != "owner" {
		a.writeErr(w, 403, "forbidden", "owner only")
		return
	}
	if !a.rateLimit("password-change:"+s.UserID, 5, time.Minute) {
		a.writeErr(w, 429, "rate_limited", "too many attempts")
		return
	}
	var body struct {
		Current string `json:"current_password"`
		Next    string `json:"new_password"`
	}
	if err := parseJSONLimit(r, &body, 16<<10); err != nil || len(body.Current) > 1024 || len(body.Next) < 12 || len(body.Next) > 1024 {
		a.writeErr(w, 400, "invalid_password", "new password must be 12..1024 bytes")
		return
	}
	if body.Next == body.Current {
		a.writeErr(w, 400, "unchanged", "choose a different password")
		return
	}
	user, err := a.Store.UserByName(s.Username)
	if err != nil || !secure.VerifyPassword(user.PasswordHash, body.Current) {
		a.writeErr(w, 401, "invalid_credentials", "current password is incorrect")
		return
	}
	hash, err := secure.HashPassword(body.Next)
	if err != nil {
		a.writeErr(w, 500, "password_hash", "could not protect new password")
		return
	}
	a.controlMu.Lock()
	defer a.controlMu.Unlock()
	if err = a.Store.SessionStillValid(s.ID); err != nil {
		a.writeErr(w, 401, "unauthorized", "session expired; sign in again")
		return
	}
	if err = a.Store.ChangePassword(s.UserID, user.PasswordHash, hash, s.Username); err != nil {
		a.writeErr(w, 409, "password_conflict", "password changed concurrently; sign in again")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "monik_session", Value: "", Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	a.writeJSON(w, 200, map[string]any{"ok": true, "sign_in_required": true, "agent_credentials_changed": false})
}
