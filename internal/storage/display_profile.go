package storage

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/jsonutil"
)

const tvProfileKey = "display_tv_v1"
const maxDisplayRevision int64 = 9007199254740990 // Exactly representable by browser numbers.

// TVLayout is presentation only. It cannot contain selectors, CSS, URLs or agent policy.
// Widths are the five preferred rem tracks; the service track is the remaining space.
type TVLayout struct {
	Widths   []float64 `json:"widths"`
	Density  string    `json:"density"`
	Autoplay bool      `json:"autoplay"`
}

func DefaultTVLayout() TVLayout { return TVLayout{Widths: []float64{9.5, 3.5, 8, 6, 8}, Density: "10"} }
func (v TVLayout) Validate() error {
	min := []float64{6.5, 3, 5, 4.5, 5}
	if len(v.Widths) != 5 || (v.Density != "10" && v.Density != "12" && v.Density != "14") {
		return fmt.Errorf("invalid TV layout")
	}
	for i, n := range v.Widths {
		if math.IsNaN(n) || math.IsInf(n, 0) || n < min[i] || n > 4096 {
			return fmt.Errorf("TV column width is out of bounds")
		}
	}
	return nil
}

// Canonical member names, required fields and non-null values prevent typos or
// newer/foreign payloads from silently changing the shared screen.
func (v *TVLayout) UnmarshalJSON(raw []byte) error {
	if err := exactObject(raw, "widths", "density", "autoplay"); err != nil {
		return err
	}
	type layout TVLayout
	var out layout
	if err := jsonutil.Unmarshal(raw, &out); err != nil {
		return err
	}
	if err := TVLayout(out).Validate(); err != nil {
		return err
	}
	*v = TVLayout(out)
	return nil
}
func exactObject(raw []byte, fields ...string) error {
	var obj map[string]json.RawMessage
	if err := jsonutil.Unmarshal(raw, &obj); err != nil {
		return err
	}
	if len(obj) != len(fields) {
		return fmt.Errorf("unexpected display profile fields")
	}
	for _, k := range fields {
		v, ok := obj[k]
		if !ok || bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return fmt.Errorf("missing display profile field")
		}
	}
	return nil
}

type TVProfile struct {
	SchemaVersion int        `json:"schema_version"`
	Revision      int64      `json:"revision"`
	Value         TVLayout   `json:"value"`
	UpdatedBy     string     `json:"updated_by"`
	UpdatedAt     *time.Time `json:"updated_at"`
	LastRequestID string     `json:"last_request_id"`
}

func validDisplayRequestID(id string) bool {
	if len(id) != 36 {
		return false
	}
	for i, c := range id {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
		} else if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func readTVProfile(db sqlExecutor) (TVProfile, error) {
	p := TVProfile{SchemaVersion: 1, Value: DefaultTVLayout()}
	var raw string
	var rev int64
	e := db.QueryRow(`SELECT value,revision FROM settings WHERE key=?`, tvProfileKey).Scan(&raw, &rev)
	if errors.Is(e, sql.ErrNoRows) {
		return p, nil
	}
	if e != nil {
		return p, e
	}
	if len(raw) > 4096 {
		return p, fmt.Errorf("stored TV profile exceeds limit")
	}
	// Persisted non-default profiles always have complete provenance.
	if e = exactObject([]byte(raw), "schema_version", "revision", "value", "updated_by", "updated_at", "last_request_id"); e != nil {
		return p, fmt.Errorf("stored TV profile is invalid: %w", e)
	}
	if e = jsonutil.Unmarshal([]byte(raw), &p); e != nil {
		return p, e
	}
	if p.SchemaVersion != 1 || rev < 1 || rev > maxDisplayRevision || rev != p.Revision || p.UpdatedAt == nil || p.UpdatedAt.IsZero() || p.UpdatedBy == "" || !validDisplayRequestID(p.LastRequestID) {
		return p, fmt.Errorf("stored TV profile identity is invalid")
	}
	return p, nil
}
func (s *Store) TVProfile() (TVProfile, error) { return readTVProfile(s.db()) }

// SaveTVProfile changes one bounded shared preference. CAS, audit and SSE hint
// commit together; no background job or permission is created. A lost response
// can be reconciled by request ID without repeating an uncertain write.
func (s *Store) SaveTVProfile(base int64, requestID, actor string, value TVLayout) (TVProfile, error) {
	var result TVProfile
	if base < 0 || base >= maxDisplayRevision || !validDisplayRequestID(requestID) || actor == "" || len(actor) > 255 {
		return result, fmt.Errorf("invalid display update identity")
	}
	if e := value.Validate(); e != nil {
		return result, e
	}
	e := s.WithTx(func(tx *sql.Tx) error {
		old, e := readTVProfile(tx)
		if e != nil {
			return e
		}
		if old.LastRequestID == requestID {
			if old.Revision != base+1 || old.UpdatedBy != actor || !reflect.DeepEqual(old.Value, value) {
				return ErrIdempotencyConflict
			}
			result = old
			return nil
		}
		if old.Revision != base {
			return ErrConflict
		}
		now := s.now()
		result = TVProfile{SchemaVersion: 1, Revision: base + 1, Value: value, UpdatedBy: actor, UpdatedAt: &now, LastRequestID: requestID}
		raw, e := json.Marshal(result)
		if e != nil {
			return e
		}
		if _, e = tx.Exec(`INSERT INTO settings(key,value,revision) VALUES(?,?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value,revision=excluded.revision`, tvProfileKey, string(raw), result.Revision); e != nil {
			return e
		}
		if _, e = tx.Exec(`INSERT INTO audit_events(at,actor,action,entity,detail) VALUES(?,?,?,?,?)`, now.Format(dbTimeFormat), actor, "display.tv.save", "tv", fmt.Sprintf("revision=%d request=%s", result.Revision, requestID)); e != nil {
			return e
		}
		_, e = tx.Exec(`INSERT INTO event_log(ts,type,entity,entity_id,revision,payload) VALUES(?,?,?,?,?,?)`, now.Format(dbTimeFormat), "display", "display", "tv", result.Revision, fmt.Sprintf(`{"revision":%d}`, result.Revision))
		return e
	})
	return result, e
}
