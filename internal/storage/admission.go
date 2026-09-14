package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type AdmissionPolicy struct {
	Revision   int64      `json:"revision"`
	OpenUntil  *time.Time `json:"open_until"`
	UpdatedBy  string     `json:"updated_by"`
	Open       bool       `json:"open"`
	ServerTime time.Time  `json:"server_time"`
}

func admissionPolicy(db sqlExecutor, now time.Time) (AdmissionPolicy, error) {
	p := AdmissionPolicy{ServerTime: now}
	var raw string
	err := db.QueryRow(`SELECT value,revision FROM settings WHERE key='admission_window_v1'`).Scan(&raw, &p.Revision)
	if errors.Is(err, sql.ErrNoRows) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	var payload struct {
		OpenUntil *time.Time `json:"open_until"`
		UpdatedBy string     `json:"updated_by"`
	}
	if err = json.Unmarshal([]byte(raw), &payload); err != nil {
		return p, fmt.Errorf("admission policy is corrupt; new registrations are blocked")
	}
	p.OpenUntil, p.UpdatedBy = payload.OpenUntil, payload.UpdatedBy
	p.Open = p.OpenUntil != nil && now.Before(*p.OpenUntil)
	return p, nil
}
func (s *Store) AdmissionPolicy() (AdmissionPolicy, error) { return admissionPolicy(s.db(), s.now()) }

// Default: closed to unknown identities. Already pending/approved identities are
// resolved BEFORE this gate, so closing admission does not strand a deployment.
func (s *Store) SetAdmission(base int64, minutes int, actor string) (AdmissionPolicy, error) {
	if base < 0 || minutes < 0 || minutes > 1440 {
		return AdmissionPolicy{}, fmt.Errorf("admission duration must be 0..1440 minutes (0 closes)")
	}
	var result AdmissionPolicy
	err := s.WithTx(func(tx *sql.Tx) error {
		p, e := admissionPolicy(tx, s.now())
		if e != nil {
			return e
		}
		if p.Revision != base {
			return fmt.Errorf("%w: admission policy changed", ErrConflict)
		}
		var until *time.Time
		if minutes > 0 {
			t := s.now().Add(time.Duration(minutes) * time.Minute)
			until = &t
		}
		raw, _ := json.Marshal(map[string]any{"open_until": until, "updated_by": actor})
		if _, e = tx.Exec(`INSERT INTO settings(key,value,revision) VALUES('admission_window_v1',?,1) ON CONFLICT(key) DO UPDATE SET value=excluded.value,revision=settings.revision+1`, string(raw)); e != nil {
			return e
		}
		result, e = admissionPolicy(tx, s.now())
		return e
	})
	return result, err
}
