package server

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alinescafs3mp-afk/monik_v2/internal/processlock"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
)

func TestControllerSecondOpenCannotMutateLiveState(t *testing.T) {
	a, _ := testApp(t)
	stamp := "2026-10-14T06:00:00.000000000Z"
	if e := a.Store.InsertOperation(&protocol.Operation{ID: "writer-fixture", Action: "update.rollout", Status: protocol.OpRunning, Revision: 1, ClientRequestKey: "writer-fixture", Actor: "owner", CreatedAt: a.Clock.Now()}, "fixture"); e != nil {
		t.Fatal(e)
	}
	if e := a.Store.InsertRelease("release", "fixture", "digest", "", "{}", "[]", true); e != nil {
		t.Fatal(e)
	}
	if _, e := a.Store.DB.Exec(`INSERT INTO release_publications(release_id,object_digest,file_inventory,expires_at,format_version) VALUES('release','digest','[]',?,1)`, stamp); e != nil {
		t.Fatal(e)
	}
	if _, e := a.Store.DB.Exec(`INSERT INTO update_rollouts(operation_id,release_id,state,revision,batch_size,observe_seconds,wave,canary_waves,stable_since,last_tick,reason,deadline,created_at,updated_at) VALUES('writer-fixture','release','running',1,2,30,0,1,?,?,'',?,?,?)`, stamp, stamp, stamp, stamp, stamp); e != nil {
		t.Fatal(e)
	}
	identity := a.ControllerID()
	files := []string{"monik.db", "monik.db-wal", "secret-master.key", "tls/ca.key", "tls/ca.crt"}
	before := map[string][]byte{}
	for _, name := range files {
		b, e := os.ReadFile(filepath.Join(a.Cfg.DataDir, name))
		if e != nil {
			t.Fatal(e)
		}
		before[name] = b
	}
	b, e := Open(a.Cfg)
	if e == nil {
		b.Close()
		t.Fatal("second writer opened")
	}
	if !strings.Contains(e.Error(), "another process owns") {
		t.Fatalf("wrong fence failure: %v", e)
	}
	for _, name := range files {
		after, e := os.ReadFile(filepath.Join(a.Cfg.DataDir, name))
		if e != nil || !bytes.Equal(before[name], after) {
			t.Fatalf("second open changed %s: %v", name, e)
		}
	}
	var stable, tick string
	if e = a.Store.DB.QueryRow(`SELECT stable_since,last_tick FROM update_rollouts WHERE operation_id='writer-fixture'`).Scan(&stable, &tick); e != nil || stable != stamp || tick != stamp || a.ControllerID() != identity {
		t.Fatal("rejected writer changed live probation/identity", e)
	}
	if e = a.Close(); e != nil {
		t.Fatal(e)
	}
	if e = a.Close(); e != nil {
		t.Fatal("idempotent close", e)
	}
	unlock, e := processlock.Acquire(filepath.Join(a.Cfg.DataDir, "controller.lock"))
	if e != nil {
		t.Fatal("close retained writer fence", e)
	}
	unlock()
}
func TestControllerFailedOpenReleasesWriterFence(t *testing.T) {
	dir := t.TempDir()
	if e := os.WriteFile(filepath.Join(dir, "secret-master.key"), []byte("protected existing state"), 0600); e != nil {
		t.Fatal(e)
	}
	if a, e := Open(Config{DataDir: dir}); e == nil {
		a.Close()
		t.Fatal("missing DB replaced")
	}
	unlock, e := processlock.Acquire(filepath.Join(dir, "controller.lock"))
	if e != nil {
		t.Fatal("failed open leaked writer fence", e)
	}
	unlock()
}
