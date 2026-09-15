package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/alinescafs3mp-afk/monik_v2/internal/clock"
)

func displayID(n int) string { return fmt.Sprintf("12345678-1234-1234-1234-%012d", n) }
func TestV19TVDefaultDoesNotWriteOrAffectMonitoring(t *testing.T) {
	s := auditStore(t)
	p, e := s.TVProfile()
	if e != nil || p.Revision != 0 || !reflect.DeepEqual(p.Value, DefaultTVLayout()) {
		t.Fatal(p, e)
	}
	for _, table := range []string{"settings", "operations", "agent_jobs", "config_revisions"} {
		var n int
		if e = s.DB.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); e != nil {
			t.Fatal(e)
		}
		if n != 0 {
			t.Fatalf("GET changed %s", table)
		}
	}
}
func TestV19TVPublishCASAuditAndIdempotency(t *testing.T) {
	s := auditStore(t)
	v := DefaultTVLayout()
	v.Widths[0] = 14
	v.Density = "12"
	v.Autoplay = true
	p, e := s.SaveTVProfile(0, displayID(1), "owner", v)
	if e != nil || p.Revision != 1 {
		t.Fatal(p, e)
	}
	same, e := s.SaveTVProfile(0, displayID(1), "owner", v)
	if e != nil || !reflect.DeepEqual(p, same) {
		t.Fatal(same, e)
	}
	if _, e = s.SaveTVProfile(0, displayID(2), "owner", v); !errors.Is(e, ErrConflict) {
		t.Fatal("stale revision accepted", e)
	}
	v.Density = "14"
	if _, e = s.SaveTVProfile(0, displayID(1), "owner", v); !errors.Is(e, ErrIdempotencyConflict) {
		t.Fatal("request ID changed its value", e)
	}
	var n int
	s.DB.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action='display.tv.save'`).Scan(&n)
	if n != 1 {
		t.Fatal("duplicate audit", n)
	}
	s.DB.QueryRow(`SELECT COUNT(*) FROM event_log WHERE type='display'`).Scan(&n)
	if n != 1 {
		t.Fatal("duplicate event", n)
	}
	if _, e = s.SaveTVProfile(1, displayID(3), "owner", v); e != nil {
		t.Fatal(e)
	}
	// A superseded old request cannot be replayed to overwrite a later edit.
	if _, e = s.SaveTVProfile(0, displayID(1), "owner", DefaultTVLayout()); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
}
func TestV19TVSurvivesStoreRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profile.db")
	s, e := Open(path, clock.Real{})
	if e != nil {
		t.Fatal(e)
	}
	p, e := s.SaveTVProfile(0, displayID(1), "owner", DefaultTVLayout())
	if e != nil {
		t.Fatal(e)
	}
	s.Close()
	s, e = Open(path, clock.Real{})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	q, e := s.TVProfile()
	if e != nil || !reflect.DeepEqual(p, q) {
		t.Fatal(p, q, e)
	}
}
func TestV19TVConcurrentScreensHaveOneWinner(t *testing.T) {
	s := auditStore(t)
	var wg sync.WaitGroup
	out := make(chan error, 2)
	for n := 1; n <= 2; n++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			v := DefaultTVLayout()
			v.Widths[0] += float64(n)
			_, e := s.SaveTVProfile(0, displayID(n), "owner", v)
			out <- e
		}(n)
	}
	wg.Wait()
	close(out)
	ok, conflict := 0, 0
	for e := range out {
		if e == nil {
			ok++
		} else if errors.Is(e, ErrConflict) {
			conflict++
		} else {
			t.Fatal(e)
		}
	}
	if ok != 1 || conflict != 1 {
		t.Fatal(ok, conflict)
	}
}
func TestV19TVAuditAndEventFailureRollbackEverything(t *testing.T) {
	for _, table := range []string{"audit_events", "event_log"} {
		t.Run(table, func(t *testing.T) {
			s := auditStore(t)
			if _, e := s.DB.Exec(`CREATE TRIGGER fail_profile BEFORE INSERT ON ` + table + ` BEGIN SELECT RAISE(ABORT,'injected'); END`); e != nil {
				t.Fatal(e)
			}
			if _, e := s.SaveTVProfile(0, displayID(1), "owner", DefaultTVLayout()); e == nil {
				t.Fatal("failure acknowledged")
			}
			p, e := s.TVProfile()
			if e != nil || p.Revision != 0 {
				t.Fatal(p, e)
			}
			var n int
			s.DB.QueryRow("SELECT COUNT(*) FROM audit_events").Scan(&n)
			if n != 0 {
				t.Fatal("partial audit")
			}
			s.DB.QueryRow("SELECT COUNT(*) FROM event_log").Scan(&n)
			if n != 0 {
				t.Fatal("partial SSE")
			}
		})
	}
}
func TestV19TVCorruptStoredValueDoesNotResetOrOverwrite(t *testing.T) {
	for _, raw := range []string{"null", "{}", `{"widths":[]}`, `{"schema_version":1,"Schema_version":1}`, `{"value":{"widths":[8,4,5,6,7],"density":"10","autoplay":false,"css":"url(evil)"}}`} {
		t.Run(raw, func(t *testing.T) {
			s := auditStore(t)
			s.SetSetting(tvProfileKey, raw)
			if _, e := s.TVProfile(); e == nil {
				t.Fatal("corrupt accepted")
			}
			if _, e := s.SaveTVProfile(1, displayID(1), "owner", DefaultTVLayout()); e == nil {
				t.Fatal("corrupt overwritten")
			}
			got, _ := s.Setting(tvProfileKey)
			if got != raw {
				t.Fatal(got)
			}
		})
	}
}
func TestV19TVLayoutStrictBoundary(t *testing.T) {
	for _, raw := range []string{`null`, `{}`, `{"widths":[8,4,6,5,6],"density":"10","autoplay":false,"extra":true}`, `{"widths":[8,4,6,5,6],"Density":"10","autoplay":false}`, `{"widths":[8,4,6,5,6],"density":"10","autoplay":null}`, `{"widths":[8,4,6,5,6],"density":"10","autoplay":true,"autoplay":false}`, `{"widths":[8,4,6,5,6],"density":"url(evil)","autoplay":false}`, `{"widths":[8,4,6,5,"5"],"density":"10","autoplay":false}`, `{"widths":[8,4,6,5,-1],"density":"10","autoplay":false}`} {
		var v TVLayout
		if e := json.Unmarshal([]byte(raw), &v); e == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	raw, _ := json.Marshal(DefaultTVLayout())
	var v TVLayout
	if e := json.Unmarshal(raw, &v); e != nil {
		t.Fatal(e)
	}
}
