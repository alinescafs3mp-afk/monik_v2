package update

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStageActivateRollback(t *testing.T) {
	dir := t.TempDir()
	cur := filepath.Join(dir, "worker")
	if err := os.WriteFile(cur, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "newsrc")
	if err := os.WriteFile(src, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}
	sum, err := SHA256File(src)
	if err != nil {
		t.Fatal(err)
	}
	staged, err := StageBinary(dir, src, sum)
	if err != nil {
		t.Fatal(err)
	}
	prev := filepath.Join(dir, "updates", "prev.bin")
	if err := Activate(cur, staged, prev); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(cur)
	if string(b) != "new" {
		t.Fatalf("current %s", b)
	}
	if err := Rollback(cur, prev); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(cur)
	if string(b) != "old" {
		t.Fatalf("rolled %s", b)
	}
}

func TestReviewActivationFailureKeepsCurrentExecutable(t *testing.T) {
	d := t.TempDir()
	cur := filepath.Join(d, "current")
	_ = os.WriteFile(cur, []byte("good"), 0755)
	if e := Activate(cur, filepath.Join(d, "missing"), filepath.Join(d, "previous")); e == nil {
		t.Fatal("missing candidate accepted")
	}
	b, _ := os.ReadFile(cur)
	if string(b) != "good" {
		t.Fatal("current executable removed before candidate ready")
	}
}
func TestReviewStagedSymlinkMustRemainInsideState(t *testing.T) {
	d := t.TempDir()
	outside := filepath.Join(t.TempDir(), "foreign")
	_ = os.WriteFile(outside, []byte("file"), 0755)
	src := filepath.Join(d, "link")
	if e := os.Symlink(outside, src); e != nil {
		t.Fatal(e)
	}
	sum, _ := SHA256File(outside)
	if _, e := StageBinary(d, src, sum); e == nil {
		t.Fatal("followed artifact symlink outside state")
	}
}
