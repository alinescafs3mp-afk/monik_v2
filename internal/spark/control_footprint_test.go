package spark

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"github.com/alinescafs3mp-afk/monik_v2/internal/secure"
	"golang.org/x/sys/unix"
)

func footprintJournalInfo(t *testing.T, c *Coordinator) os.FileInfo {
	t.Helper()
	info, err := os.Stat(c.path)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func footprintSave(t *testing.T, c *Coordinator) {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.saveLocked(); err != nil {
		t.Fatal(err)
	}
}

func footprintAssertJournal(t *testing.T, c *Coordinator) {
	t.Helper()
	c.mu.Lock()
	want, err := json.Marshal(c.journal)
	c.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	got, err := readPrivateFile(c.path, 4<<20, uint32(os.Geteuid()))
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("persisted journal differs from full current journal: %v", err)
	}
}

func TestControlFootprintUnchangedSnapshotPreservesJournal(t *testing.T) {
	c, _, _ := controlFixture(t)
	before := footprintJournalInfo(t, c)
	first, err := c.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !second.Inventory.ObservedAt.After(first.Inventory.ObservedAt) || !second.States[0].ObservedAt.After(first.States[0].ObservedAt) {
		t.Fatal("unchanged durable state stopped fresh public observations")
	}
	after := footprintJournalInfo(t, c)
	if !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("unchanged snapshots republished the journal")
	}
	footprintAssertJournal(t, c)
}

func TestControlFootprintChangedStateRemainsDurable(t *testing.T) {
	changes := map[string]func(*Coordinator, Profile){
		"generation-and-state": func(c *Coordinator, p Profile) {
			r := c.journal.Profiles[p.ID]
			r.Generation++
			r.Identity, r.State = "fresh-instance", "running"
			c.journal.Profiles[p.ID] = r
		},
		"reservation-and-timestamps": func(c *Coordinator, p Profile) {
			r := c.journal.Profiles[p.ID]
			r.Reserved = true
			r.StartingAt = time.Now().UTC()
			r.ReservationUntil = r.StartingAt.Add(time.Minute)
			c.journal.Profiles[p.ID] = r
		},
		"authority": func(c *Coordinator, _ Profile) {
			c.journal.Epoch = "changed-control-epoch"
			c.journal.PolicyHash = "changed-policy-hash"
		},
		"receipt-and-restart-link": func(c *Coordinator, p Profile) {
			at := time.Now().UTC()
			c.journal.Attempts["stopped-start"] = attempt{
				Request: ActionRequest{JobID: "stopped-start", Params: protocol.ModelActionParams{ProfileID: p.ID}},
				Stage:   "done", At: at, StoppedByJobID: "verified-stop",
				Receipt: protocol.JobReceipt{JobID: "stopped-start", Status: protocol.TargetFailed, Stage: "start_cancelled_after_stop", VerifiedAt: &at},
			}
		},
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			c, _, p := controlFixture(t)
			before := footprintJournalInfo(t, c)
			c.mu.Lock()
			change(c, p)
			c.mu.Unlock()
			footprintSave(t, c)
			after := footprintJournalInfo(t, c)
			if os.SameFile(before, after) {
				t.Fatal("changed durable state skipped publication")
			}
			footprintAssertJournal(t, c)
			footprintSave(t, c)
			if !os.SameFile(after, footprintJournalInfo(t, c)) {
				t.Fatal("successfully persisted state was republished unchanged")
			}
		})
	}
}

func TestControlFootprintRejectsJournalDriftAndUnsafePaths(t *testing.T) {
	changes := map[string]func(*testing.T, *Coordinator){
		"deleted": func(t *testing.T, c *Coordinator) {
			if err := os.Remove(c.path); err != nil {
				t.Fatal(err)
			}
		},
		"corrupt": func(t *testing.T, c *Coordinator) {
			if err := os.WriteFile(c.path, []byte("{broken"), 0600); err != nil {
				t.Fatal(err)
			}
		},
		"foreign-private-replacement": func(t *testing.T, c *Coordinator) {
			if err := secure.AtomicWrite(c.path, []byte(`{"epoch":"foreign","attempts":{},"profiles":{}}`), 0600); err != nil {
				t.Fatal(err)
			}
		},
		"public-permissions": func(t *testing.T, c *Coordinator) {
			if err := os.Chmod(c.path, 0644); err != nil {
				t.Fatal(err)
			}
		},
		"symlink": func(t *testing.T, c *Coordinator) {
			saved := c.path + ".saved"
			if err := os.Rename(c.path, saved); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(saved, c.path); err != nil {
				t.Fatal(err)
			}
		},
		"hardlink": func(t *testing.T, c *Coordinator) {
			if err := os.Link(c.path, c.path+".link"); err != nil {
				t.Fatal(err)
			}
		},
		"directory": func(t *testing.T, c *Coordinator) {
			if err := os.Rename(c.path, c.path+".saved"); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(c.path, 0700); err != nil {
				t.Fatal(err)
			}
		},
		"fifo": func(t *testing.T, c *Coordinator) {
			if err := os.Remove(c.path); err != nil {
				t.Fatal(err)
			}
			if err := unix.Mkfifo(c.path, 0600); err != nil {
				t.Fatal(err)
			}
		},
		"oversized": func(t *testing.T, c *Coordinator) {
			if err := os.WriteFile(c.path, make([]byte, (4<<20)+1), 0600); err != nil {
				t.Fatal(err)
			}
		},
		"changed-path": func(t *testing.T, c *Coordinator) {
			c.path = filepath.Join(t.TempDir(), "different-journal.json")
		},
	}
	for name, change := range changes {
		for _, changedState := range []bool{false, true} {
			label := name + "/unchanged-state"
			if changedState {
				label = name + "/changed-state"
			}
			t.Run(label, func(t *testing.T) {
				c, _, p := controlFixture(t)
				change(t, c)
				before, beforeErr := os.Lstat(c.path)
				c.mu.Lock()
				if changedState {
					r := c.journal.Profiles[p.ID]
					r.Generation++
					c.journal.Profiles[p.ID] = r
				}
				at := time.Now()
				err := c.saveLocked()
				c.mu.Unlock()
				if err == nil {
					t.Fatal("unsafe or drifted journal accepted")
				}
				if time.Since(at) > time.Second {
					t.Fatal("unsafe path verification blocked")
				}
				after, afterErr := os.Lstat(c.path)
				if os.IsNotExist(beforeErr) {
					if !os.IsNotExist(afterErr) {
						t.Fatal("deleted journal recreated without reconciliation")
					}
				} else if beforeErr != nil || afterErr != nil || !os.SameFile(before, after) || before.Mode() != after.Mode() || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
					t.Fatalf("unsafe/drifted evidence was replaced or changed: before=%v after=%v", beforeErr, afterErr)
				}
			})
		}
	}
}

func TestControlFootprintFailedPublicationForcesRetry(t *testing.T) {
	for _, publishBeforeError := range []bool{false, true} {
		for _, changedState := range []bool{false, true} {
			name := "before-publication"
			if publishBeforeError {
				name = "after-publication"
			}
			if changedState {
				name += "/changed-state"
			} else {
				name += "/unchanged-state"
			}
			t.Run(name, func(t *testing.T) {
				c, _, p := controlFixture(t)
				calls := 0
				c.mu.Lock()
				previous := bytes.Clone(c.savedJournal)
				if changedState {
					r := c.journal.Profiles[p.ID]
					r.Generation++
					c.journal.Profiles[p.ID] = r
				}
				c.writeJournal = func(path string, data []byte, mode os.FileMode) error {
					calls++
					if publishBeforeError {
						if err := secure.AtomicWrite(path, data, mode); err != nil {
							return err
						}
					}
					return errors.New("synthetic publication failure")
				}
				err := c.saveLocked()
				if err == nil || calls != 1 || !bytes.Equal(previous, c.savedJournal) || c.uncertainJournal == nil {
					c.mu.Unlock()
					t.Fatal("failed writer bypassed or cached as successful")
				}
				c.writeJournal = nil
				c.mu.Unlock()
				beforeRetry := footprintJournalInfo(t, c)
				footprintSave(t, c)
				afterRetry := footprintJournalInfo(t, c)
				if os.SameFile(beforeRetry, afterRetry) {
					t.Fatal("uncertain publication skipped successful synced retry")
				}
				footprintAssertJournal(t, c)
				footprintSave(t, c)
				if !os.SameFile(afterRetry, footprintJournalInfo(t, c)) {
					t.Fatal("successful retry did not permit the next unchanged no-op")
				}
			})
		}
	}
}

func TestControlFootprintCorruptionAfterFailedPublicationIsPreserved(t *testing.T) {
	c, _, _ := controlFixture(t)
	c.mu.Lock()
	c.writeJournal = func(path string, data []byte, mode os.FileMode) error {
		if err := secure.AtomicWrite(path, data, mode); err != nil {
			return err
		}
		return errors.New("synthetic failure after rename")
	}
	if err := c.saveLocked(); err == nil {
		c.mu.Unlock()
		t.Fatal("synthetic failed publication accepted")
	}
	c.writeJournal = nil
	c.mu.Unlock()
	corrupt := []byte("intervening corruption")
	if err := os.WriteFile(c.path, corrupt, 0600); err != nil {
		t.Fatal(err)
	}
	c.mu.Lock()
	err := c.saveLocked()
	c.mu.Unlock()
	if err == nil {
		t.Fatal("retry overwrote intervening corruption")
	}
	got, err := os.ReadFile(c.path)
	if err != nil || !bytes.Equal(got, corrupt) {
		t.Fatal("corruption evidence lost")
	}
}

func TestControlFootprintConsecutivePublicationFailuresRemainRetryable(t *testing.T) {
	c, _, p := controlFixture(t)
	c.mu.Lock()
	calls := 0
	c.writeJournal = func(path string, data []byte, mode os.FileMode) error {
		calls++
		if calls == 1 {
			if err := secure.AtomicWrite(path, data, mode); err != nil {
				return err
			}
		}
		return errors.New("synthetic consecutive publication failure")
	}
	for i := 0; i < 2; i++ {
		r := c.journal.Profiles[p.ID]
		r.Generation++
		c.journal.Profiles[p.ID] = r
		if err := c.saveLocked(); err == nil {
			c.mu.Unlock()
			t.Fatal("synthetic failure accepted")
		}
	}
	c.writeJournal = nil
	c.mu.Unlock()
	footprintSave(t, c)
	footprintAssertJournal(t, c)
}
