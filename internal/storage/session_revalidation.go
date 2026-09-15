package storage

import (
	"database/sql"
	"errors"
	"time"
)

// RevalidateSession refreshes authorization after a request body or lock wait.
// A previously authenticated session must retain its principal, token and role;
// a role change requires a new request, even when it would increase authority.
func (s *Store) RevalidateSession(previous *Session) (*Session, error) {
	if previous == nil || previous.ID == "" {
		return nil, ErrNotFound
	}
	current := &Session{}
	var expires string
	var recent sql.NullString
	e := s.db().QueryRow(`SELECT s.id,s.user_id,s.token_hash,s.csrf,s.expires_at,s.recent_auth_until,u.role,u.username FROM admin_sessions s JOIN admin_users u ON u.id=s.user_id WHERE s.id=?`, previous.ID).Scan(&current.ID, &current.UserID, &current.TokenHash, &current.CSRF, &expires, &recent, &current.Role, &current.Username)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if e != nil {
		return nil, e
	}
	current.ExpiresAt, e = time.Parse(time.RFC3339Nano, expires)
	if e != nil || !s.now().Before(current.ExpiresAt) {
		return nil, ErrNotFound
	}
	if current.UserID != previous.UserID || current.TokenHash != previous.TokenHash || current.CSRF != previous.CSRF || current.Role != previous.Role || current.Username != previous.Username {
		return nil, ErrConflict
	}
	if recent.Valid {
		at, e := time.Parse(time.RFC3339Nano, recent.String)
		if e != nil {
			return nil, e
		}
		current.RecentAuthUntil = &at
	}
	return current, nil
}
