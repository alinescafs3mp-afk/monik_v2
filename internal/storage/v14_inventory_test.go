package storage

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/clock"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
)

func inventoryStore(t *testing.T) (*Store, *clock.Fake) {
	t.Helper()
	clk := clock.NewFake(time.Now().UTC())
	s, e := Open(filepath.Join(t.TempDir(), "inventory.db"), clk)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	auditAgent(t, s, "h")
	return s, clk
}
func inventoryReport(at time.Time, seq int64, targets []string, complete bool, confirmed ...protocol.DiscoveredEndpoint) protocol.AgentReport {
	return protocol.AgentReport{SchemaVersion: 3, AgentID: "h", SessionID: "s", Sequence: seq, ObservedAt: at, IsLive: true, Discovery: &protocol.DiscoveryDelta{InventoryVersion: 1, Kind: "snapshot", StartedAt: at.Add(-time.Millisecond), EndedAt: at, ListenerTargets: targets, ListenerCoverageComplete: complete, ListenerCount: len(targets), Confirmed: confirmed, CoverageComplete: true}}
}
func inventoryEndpoint(target string) protocol.DiscoveredEndpoint {
	return protocol.DiscoveredEndpoint{ServiceID: "agent-random-id", DialTarget: target, URL: "http://" + target, Source: "listener+http", SpeaksHTTP: true}
}
func acceptInventory(t *testing.T, s *Store, r protocol.AgentReport) AcceptedReport {
	t.Helper()
	a, e := s.AcceptReport(r)
	if e != nil {
		t.Fatal(e)
	}
	return a
}
func TestV14StableIdentityPresenceAndOwnerPreferences(t *testing.T) {
	s, c := inventoryStore(t)
	ep := inventoryEndpoint("127.0.0.1:9010")
	r := acceptInventory(t, s, inventoryReport(c.Now(), 1, []string{ep.DialTarget}, true, ep))
	id := r.Endpoints[0].ServiceID
	name := "API владельца"
	if e := s.RenameService(id, name, nil); e != nil {
		t.Fatal(e)
	}
	if e := s.SetServicePinned(id, true, nil); e != nil {
		t.Fatal(e)
	}
	for i := 2; i <= 5; i++ {
		c.Advance(time.Minute)
		ep.ServiceID = fmt.Sprint("random-", i)
		ep.PID = i
		ep.URL = "https://" + ep.DialTarget
		ep.Source = "listener+https"
		acceptInventory(t, s, inventoryReport(c.Now(), int64(i), []string{ep.DialTarget}, true, ep))
	}
	rows, e := s.Services("h")
	if e != nil || len(rows) != 1 {
		t.Fatal(rows, e)
	}
	if rows[0].ID != id || rows[0].DisplayName != name || !rows[0].Pinned || rows[0].InventoryState != "present" {
		t.Fatalf("identity/preferences changed: %+v", rows[0])
	}
	// Being present but unprobed (paused, authorization problem, etc.) still proves a socket exists.
	c.Advance(time.Minute)
	rep := inventoryReport(c.Now(), 6, []string{ep.DialTarget}, true)
	rep.Discovery.Unresolved = []protocol.UnresolvedCandidate{{DialTarget: ep.DialTarget, Reason: "no HTTP response"}}
	acceptInventory(t, s, rep)
	row, e := s.Service(id)
	if e != nil || row.InventoryState != "present" || row.LastDiscoveredAt == nil || !row.LastDiscoveredAt.Equal(c.Now()) {
		t.Fatal(row, e)
	}
}
func TestV14AbsenceNeedsDistinctCompleteScansAndGrace(t *testing.T) {
	s, c := inventoryStore(t)
	ep := inventoryEndpoint("127.0.0.1:9010")
	first := acceptInventory(t, s, inventoryReport(c.Now(), 1, []string{ep.DialTarget}, true, ep))
	id := first.Endpoints[0].ServiceID
	state := func(want string) {
		t.Helper()
		row, e := s.Service(id)
		if e != nil || row.InventoryState != want {
			t.Fatalf("want %s: %+v %v", want, row, e)
		}
	}
	c.Advance(time.Minute)
	miss := inventoryReport(c.Now(), 2, nil, true)
	acceptInventory(t, s, miss)
	state("unconfirmed")
	if a := acceptInventory(t, s, miss); !a.Duplicate {
		t.Fatal("replay not deduplicated")
	}
	state("unconfirmed")
	c.Advance(20 * time.Second)
	acceptInventory(t, s, inventoryReport(c.Now(), 3, nil, true))
	state("unconfirmed")
	c.Advance(time.Minute)
	acceptInventory(t, s, inventoryReport(c.Now(), 4, nil, false))
	state("unconfirmed")
	// A full HTTP-budget-truncated scan still has an independent complete socket inventory.
	c.Advance(time.Minute)
	r := inventoryReport(c.Now(), 5, nil, true)
	r.Discovery.CoverageComplete = false
	r.Discovery.Truncated = true
	acceptInventory(t, s, r)
	state("missing")
	old := miss
	old.Sequence = 6
	old.ObservedAt = c.Now()
	old.Discovery.Confirmed = []protocol.DiscoveredEndpoint{ep}
	old.Discovery.ListenerTargets = []string{ep.DialTarget}
	old.Discovery.ListenerCount = 1
	acceptInventory(t, s, old)
	state("missing")
	c.Advance(time.Minute)
	fresh := inventoryReport(c.Now(), 7, []string{ep.DialTarget}, true)
	acceptInventory(t, s, fresh)
	state("present")
}
func TestV14BackfillAndLegacyCannotRetire(t *testing.T) {
	s, c := inventoryStore(t)
	ep := inventoryEndpoint("127.0.0.1:9010")
	id := acceptInventory(t, s, inventoryReport(c.Now(), 1, []string{ep.DialTarget}, true, ep)).Endpoints[0].ServiceID
	for i := 2; i < 5; i++ {
		c.Advance(time.Minute)
		r := inventoryReport(c.Now(), int64(i), nil, true)
		if i == 2 {
			r.IsLive = false
		} else {
			r.Discovery.InventoryVersion = 0
		}
		acceptInventory(t, s, r)
	}
	row, e := s.Service(id)
	if e != nil || row.InventoryState != "present" {
		t.Fatal(row, e)
	}
	for i := 5; i < 7; i++ {
		c.Advance(time.Minute)
		acceptInventory(t, s, inventoryReport(c.Now(), int64(i), nil, true))
	}
	row, _ = s.Service(id)
	if row.InventoryState != "missing" {
		t.Fatal(row)
	}
	c.Advance(time.Minute)
	r := inventoryReport(c.Now(), 7, []string{ep.DialTarget}, true, ep)
	r.Discovery.InventoryVersion = 0
	acceptInventory(t, s, r)
	row, _ = s.Service(id)
	if row.InventoryState != "present" {
		t.Fatal("legacy positive evidence did not revive", row)
	}
}
func TestV14InventoryFailureRollsBackTelemetryAndWatermark(t *testing.T) {
	s, c := inventoryStore(t)
	ep := inventoryEndpoint("127.0.0.1:9010")
	id := acceptInventory(t, s, inventoryReport(c.Now(), 1, []string{ep.DialTarget}, true, ep)).Endpoints[0].ServiceID
	_, e := s.DB.Exec(`CREATE TRIGGER deny_inventory BEFORE UPDATE ON service_presence BEGIN SELECT RAISE(ABORT,'injected presence failure'); END`)
	if e != nil {
		t.Fatal(e)
	}
	c.Advance(time.Minute)
	r := inventoryReport(c.Now(), 2, nil, true)
	if _, e = s.AcceptReport(r); e == nil {
		t.Fatal("transaction accepted after injected failure")
	}
	var n int
	s.DB.QueryRow(`SELECT COUNT(*) FROM ingest_receipts WHERE seq=2`).Scan(&n)
	if n != 0 {
		t.Fatal("receipt committed on failure")
	}
	ag, e := s.Agent("h")
	if e != nil || ag.LastSeq != 1 {
		t.Fatal(ag, e)
	}
	row, e := s.Service(id)
	if e != nil || row.InventoryState != "present" {
		t.Fatal(row, e)
	}
	s.DB.Exec(`DROP TRIGGER deny_inventory`)
	acceptInventory(t, s, r)
}
func TestV14SnapshotSurvivesRestartAndNeverDeletesHistory(t *testing.T) {
	s, c := inventoryStore(t)
	ep := inventoryEndpoint("127.0.0.1:9010")
	id := acceptInventory(t, s, inventoryReport(c.Now(), 1, []string{ep.DialTarget}, true, ep)).Endpoints[0].ServiceID
	if e := s.InsertCheckObs(protocol.CheckObservation{ServiceID: id, CheckID: "c", ObservedAt: c.Now(), Transport: "ok", Vantage: "agent/local", Quality: protocol.QualityOK}, "h"); e != nil {
		t.Fatal(e)
	}
	c.Advance(time.Minute)
	acceptInventory(t, s, inventoryReport(c.Now(), 2, nil, true))
	path := s.Path()
	s.Close()
	reopened, e := Open(path, c)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	c.Advance(time.Minute)
	acceptInventory(t, reopened, inventoryReport(c.Now(), 3, nil, true))
	row, e := reopened.Service(id)
	if e != nil || row.InventoryState != "missing" {
		t.Fatal(row, e)
	}
	obs, e := reopened.LatestCheckObs(id)
	if e != nil || obs == nil {
		t.Fatal("history lost", e)
	}
}
func TestV14InvalidInventoryRejectedBeforeWriting(t *testing.T) {
	for name, mutate := range map[string]func(*protocol.DiscoveryDelta){
		"duplicate": func(d *protocol.DiscoveryDelta) {
			d.ListenerTargets = []string{"127.0.0.1:9001", "127.0.0.1:9001"}
			d.ListenerCount = 2
		},
		"name":     func(d *protocol.DiscoveryDelta) { d.ListenerTargets = []string{"localhost:9001"} },
		"wildcard": func(d *protocol.DiscoveryDelta) { d.ListenerTargets = []string{"0.0.0.0:9001"} },
		"foreign-target": func(d *protocol.DiscoveryDelta) {
			d.Confirmed = []protocol.DiscoveredEndpoint{inventoryEndpoint("127.0.0.1:9002")}
		},
		"time-reversed":  func(d *protocol.DiscoveryDelta) { d.StartedAt = d.EndedAt.Add(time.Second) },
		"version":        func(d *protocol.DiscoveryDelta) { d.InventoryVersion = 2 },
		"listener-count": func(d *protocol.DiscoveryDelta) { d.ListenerCount = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			s, c := inventoryStore(t)
			r := inventoryReport(c.Now(), 1, []string{"127.0.0.1:9001"}, true)
			mutate(r.Discovery)
			if _, e := s.AcceptReport(r); e == nil {
				b, _ := json.Marshal(r)
				t.Fatal("accepted invalid inventory", string(b))
			}
			var n int
			s.DB.QueryRow(`SELECT COUNT(*) FROM ingest_receipts`).Scan(&n)
			if n != 0 {
				t.Fatal("committed invalid receipt")
			}
		})
	}
}
