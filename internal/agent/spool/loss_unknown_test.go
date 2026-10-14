package spool

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/jsonutil"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
)

// An operator can preserve a verified lower bound without claiming that a
// damaged prior ledger's unknown total has been reconstructed.
const priorUnknownFixture = `{"schema":2,"dropped":163673,"from":"2026-10-04T08:57:07.278557247Z","to":"2026-10-13T20:38:32.561063747Z","prior_unknown":true}`

func writePriorUnknownFixture(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, lossLedgerName), []byte(priorUnknownFixture), 0600); err != nil {
		t.Fatal(err)
	}
}

func assertPriorUnknownStatus(t *testing.T, s *Store, dropped int64) protocol.SpoolStatus {
	t.Helper()
	status := s.Status()
	// Decode the actual wire JSON so this regression also compiles against the
	// original source and proves its pre-change failure at Open.
	raw, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		DroppedUnknown bool `json:"dropped_unknown"`
	}
	if err = json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	if status.Dropped != dropped || !wire.DroppedUnknown || !strings.Contains(status.Error, "total unknown") || !strings.Contains(status.Error, "known lower bound") {
		t.Fatalf("lost unknown-total qualifier or known count: %s", raw)
	}
	// Older wire readers still receive the unconditional Error qualifier.
	var legacy struct {
		Dropped int64  `json:"dropped"`
		Error   string `json:"error"`
	}
	if err = jsonutil.Unmarshal(raw, &legacy); err != nil || legacy.Dropped != dropped || legacy.Error != status.Error {
		t.Fatalf("legacy status decoder lost loss evidence: %+v %v", legacy, err)
	}
	return status
}

func assertPriorUnknownLedger(t *testing.T, dir string, dropped int64) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, lossLedgerName))
	if err != nil {
		t.Fatal(err)
	}
	var ledger struct {
		Schema       int      `json:"schema"`
		Dropped      int64    `json:"dropped"`
		PriorUnknown bool     `json:"prior_unknown"`
		Pending      []string `json:"pending"`
	}
	if err = jsonutil.Unmarshal(raw, &ledger); err != nil || ledger.Schema != 2 || !ledger.PriorUnknown || ledger.Dropped != dropped || len(ledger.Pending) != 0 {
		t.Fatalf("durable unknown evidence erased or retirement unfinished: %s %v", raw, err)
	}
}

func TestPriorUnknownLossStatusSurvivesRetirementAndReopen(t *testing.T) {
	dir := t.TempDir()
	writePriorUnknownFixture(t, dir)
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	status := assertPriorUnknownStatus(t, s, 163673)
	if status.DropFrom == nil || status.DropTo == nil || status.DropFrom.Format(time.RFC3339Nano) != "2026-10-04T08:57:07.278557247Z" || status.DropTo.Format(time.RFC3339Nano) != "2026-10-13T20:38:32.561063747Z" {
		t.Fatalf("known loss interval changed: %+v", status)
	}
	r := report(1)
	if err = s.Push(r); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-protocol.SpoolMaxAge - time.Minute)
	if err = os.Chtimes(filepath.Join(dir, "records-v2", recordName(r)), past, past); err != nil {
		t.Fatal(err)
	}
	if rows, err := s.List(); err != nil || len(rows) != 0 {
		t.Fatal(rows, err)
	}
	assertPriorUnknownStatus(t, s, 163674)
	assertPriorUnknownLedger(t, dir, 163674)
	for i := 0; i < 2; i++ {
		s, err = Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.List(); err != nil {
			t.Fatal(err)
		}
		assertPriorUnknownStatus(t, s, 163674)
	}
}

func TestPriorUnknownPendingRetirementIsNotRecounted(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "records-v2"), 0700); err != nil {
		t.Fatal(err)
	}
	r := report(1)
	rel := "records-v2/" + recordName(r)
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, filepath.FromSlash(rel)), raw, 0600); err != nil {
		t.Fatal(err)
	}
	var ledger map[string]any
	if err = json.Unmarshal([]byte(priorUnknownFixture), &ledger); err != nil {
		t.Fatal(err)
	}
	ledger["dropped"], ledger["pending"] = 163674, []string{rel}
	raw, err = json.Marshal(ledger)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, lossLedgerName), raw, 0600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		s, err := Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		if status := assertPriorUnknownStatus(t, s, 163674); status.Items != 0 {
			t.Fatal("pending retirement was not completed", status)
		}
		assertPriorUnknownLedger(t, dir, 163674)
	}
}

func TestPriorUnknownWriteFailureRetainsDataAndQualifier(t *testing.T) {
	dir := t.TempDir()
	writePriorUnknownFixture(t, dir)
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	r := report(1)
	if err = s.Push(r); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "records-v2", recordName(r))
	past := time.Now().Add(-protocol.SpoolMaxAge - time.Minute)
	if err = os.Chtimes(path, past, past); err != nil {
		t.Fatal(err)
	}
	ledgerPath := filepath.Join(dir, lossLedgerName)
	if err = os.Rename(ledgerPath, ledgerPath+".preserved"); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(ledgerPath, 0700); err != nil {
		t.Fatal(err)
	}
	if err = s.Push(report(2)); err == nil {
		t.Fatal("failed loss publication reported success")
	}
	if _, err = os.Stat(path); err != nil {
		t.Fatal("unaccounted data retired after failed publication", err)
	}
	if status := assertPriorUnknownStatus(t, s, 163673); !strings.Contains(status.Error, "spool write/retention failed") {
		t.Fatal("operational error disappeared", status)
	}
	if _, err = s.List(); err == nil {
		t.Fatal("failed retention reported success")
	}
	assertPriorUnknownStatus(t, s, 163673)
	if err = os.Remove(ledgerPath); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(ledgerPath+".preserved", ledgerPath); err != nil {
		t.Fatal(err)
	}
	if _, err = s.List(); err != nil {
		t.Fatal(err)
	}
	status := assertPriorUnknownStatus(t, s, 163674)
	if strings.Contains(status.Error, "retention failed") {
		t.Fatal("cleared operational error remained", status)
	}
	assertPriorUnknownLedger(t, dir, 163674)
}

func TestLossRecoveryHealthyJSONUnchanged(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(s.Status())
	if err != nil || string(raw) != `{"bytes":0,"items":0,"dropped":0}` {
		t.Fatalf("healthy wire bytes changed: %s %v", raw, err)
	}
	raw, err = json.Marshal(s.lossSnapshotLocked())
	if err != nil || string(raw) != `{"schema":1,"dropped":0}` {
		t.Fatalf("healthy empty ledger bytes changed: %s %v", raw, err)
	}
	dir := t.TempDir()
	healthy := strings.Replace(strings.Replace(priorUnknownFixture, `"schema":2`, `"schema":1`, 1), `,"prior_unknown":true`, "", 1)
	if err = os.WriteFile(filepath.Join(dir, lossLedgerName), []byte(healthy), 0600); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	raw, err = json.Marshal(s.lossSnapshotLocked())
	if err != nil || string(raw) != healthy || s.Status().Error != "" {
		t.Fatalf("healthy existing ledger contract changed: %s %v", raw, err)
	}
}

func TestPriorUnknownInvalidLedgerStillRejectsWithoutRepair(t *testing.T) {
	cases := map[string]string{
		"corrupt":        `{"schema":1,"dropped":163673,`,
		"old_schema":     strings.Replace(priorUnknownFixture, `"schema":2`, `"schema":1`, 1),
		"missing_flag":   strings.Replace(priorUnknownFixture, `,"prior_unknown":true`, "", 1),
		"wrong_flag":     strings.Replace(priorUnknownFixture, `"prior_unknown":true`, `"prior_unknown":"true"`, 1),
		"negative":       strings.Replace(priorUnknownFixture, `"dropped":163673`, `"dropped":-1`, 1),
		"unsafe_path":    strings.TrimSuffix(priorUnknownFixture, "}") + `,"pending":["../outside.json"]}`,
		"duplicate_path": strings.TrimSuffix(priorUnknownFixture, "}") + `,"pending":["sentinel.json","sentinel.json"]}`,
	}
	for name, pendingCount := range map[string]int{"batch_limit": lossBatchLimit + 1, "count_limit": 2} {
		var ledger map[string]any
		if err := json.Unmarshal([]byte(priorUnknownFixture), &ledger); err != nil {
			t.Fatal(err)
		}
		paths := make([]string, pendingCount)
		for i := range paths {
			paths[i] = strings.Repeat("a", i+1) + ".json"
		}
		ledger["pending"] = paths
		if name == "count_limit" {
			ledger["dropped"] = 1
		}
		raw, err := json.Marshal(ledger)
		if err != nil {
			t.Fatal(err)
		}
		cases[name] = string(raw)
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, lossLedgerName)
			if err := os.WriteFile(path, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "sentinel.json"), []byte("untouched"), 0600); err != nil {
				t.Fatal(err)
			}
			if s, err := Open(dir); err == nil {
				if name == "old_schema" {
					assertPriorUnknownStatus(t, s, 163673)
				}
				t.Fatal("invalid ledger accepted or silently repaired")
			}
			if after, err := os.ReadFile(path); err != nil || string(after) != body {
				t.Fatal("invalid ledger overwritten", err)
			}
			if after, err := os.ReadFile(filepath.Join(dir, "sentinel.json")); err != nil || string(after) != "untouched" {
				t.Fatal("record removed before ledger validation", err)
			}
		})
	}
}
