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
