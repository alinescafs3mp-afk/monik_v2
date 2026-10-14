//go:build linux

package spark

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
)

func TestExistingLogsPinnedReadWitnessRejectsReplacementBeforeGuardConstruction(t *testing.T) {
	root, _, owner := existingLogsFixture(t, "safe\n", "safe\n")
	path := filepath.Join(root, "values")
	old := []byte("old-key-material-aa1122\n")
	if e := os.WriteFile(path, old, 0600); e != nil {
		t.Fatal(e)
	}
	f, witness, e := openExistingLogFile(path, owner, true)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	if e = os.Rename(path, path+".old"); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(path, []byte("new-key-material-zz9988\n"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = existingRedactionGuard(path, owner, witness); e == nil {
		t.Fatal("replacement became guard baseline")
	}
	if _, e = existingRedactionGuard(path, owner); e == nil {
		t.Fatal("unverified pathname guard baseline admitted")
	}
}

func TestExistingLogsHeldRedactorWitnessRejectsNewReaderAndTransportBoundary(t *testing.T) {
	root, id, owner := existingLogsFixture(t, "new-key-material-zz9988\n", "safe\n")
	path := filepath.Join(root, "values")
	old := []byte("old-key-material-aa1122\n")
	if e := os.WriteFile(path, old, 0600); e != nil {
		t.Fatal(e)
	}
	pin := sha256.Sum256(old)
	held, e := pinnedExistingLogRedactor(path, hex.EncodeToString(pin[:]), owner)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Rename(path, path+".old"); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(path, []byte("new-key-material-zz9988\n"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = pinnedExistingLogRedactor(path, hex.EncodeToString(pin[:]), owner); e == nil {
		t.Fatal("reader accepted different unpinned keys")
	}
	r, lines, done := startExistingFixtureReader(t, root, id, owner, held.existingGuard)
	defer r.Close()
	if e = <-done; e == nil {
		t.Fatal("held witness did not revoke stream")
	}
	for line := range lines {
		t.Fatal("new key reached reader", line)
	}
	h := NewLogHub()
	viewer := make(chan protocol.SparkLogRecord, 1)
	h.profiles[id] = &logProfile{viewers: map[int]chan protocol.SparkLogRecord{1: viewer}, cancel: func() {}}
	if e = h.appendRedacted(id, held, []byte("new-key-material-zz9988"), false); e == nil {
		t.Fatal("held keys accepted new key at transport boundary")
	}
	if len(h.profiles[id].records) != 0 || len(viewer) != 0 {
		t.Fatal("new key retained/transported before witness check")
	}
}

func TestExistingLogsSamePinnedBytesNewInodeClosesHeldGuard(t *testing.T) {
	root, _, owner := existingLogsFixture(t, "safe\n", "safe\n")
	path := filepath.Join(root, "values")
	value := []byte("same-approved-key-material\n")
	if e := os.WriteFile(path, value, 0600); e != nil {
		t.Fatal(e)
	}
	pin := sha256.Sum256(value)
	held, e := pinnedExistingLogRedactor(path, hex.EncodeToString(pin[:]), owner)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Rename(path, path+".old"); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(path, value, 0600); e != nil {
		t.Fatal(e)
	}
	readerCopy, e := pinnedExistingLogRedactor(path, hex.EncodeToString(pin[:]), owner)
	if e != nil {
		t.Fatal(e)
	}
	if e = readerCopy.existingGuard(); e != nil {
		t.Fatal(e)
	}
	if e = held.existingGuard(); e == nil {
		t.Fatal("same-byte replacement silently rebound held redactor")
	}
}
