package storage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestV12DatabaseFilenameIsNotAURIQuery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "host?mode=memory#state.db")
	s, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetSetting("canary", "survives-reopen"); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(path); err != nil {
		t.Fatalf("database was not written to selected filename: %v", err)
	}
	s, err = Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	v, err := s.Setting("canary")
	if err != nil || v != "survives-reopen" {
		t.Fatalf("database contents lost on reopen: %q %v", v, err)
	}
}
func TestV12MissingFrozenMemberJobIsNotAnEmptySuccessfulRollout(t *testing.T) {
	f := newRolloutFixture(t, "linux/amd64", "linux/amd64")
	f.publish(t)
	id, err := f.s.JobIDFor("roll", "b")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.DB.Exec(`DELETE FROM agent_jobs WHERE job_id=?`, id); err != nil {
		t.Fatal(err)
	}
	if r, err := f.s.Rollout("roll"); err == nil {
		t.Fatalf("missing frozen member was silently omitted: %+v", r.Members)
	}
	if _, err = f.s.ClaimPendingJobs("a", 8); err == nil {
		t.Fatal("corrupt rollout continued dispatching")
	}
}
