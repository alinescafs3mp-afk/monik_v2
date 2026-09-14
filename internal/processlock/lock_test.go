package processlock

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStateLockExclusiveAndReusable(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state.lock")
	unlock, e := Acquire(p)
	if e != nil {
		t.Fatal(e)
	}
	if second, e := Acquire(p); e == nil {
		second()
		unlock()
		t.Fatal("duplicate ownership")
	}
	before, e := os.Stat(p)
	if e != nil {
		t.Fatal(e)
	}
	unlock()
	again, e := Acquire(p)
	if e != nil {
		t.Fatal(e)
	}
	defer again()
	after, e := os.Stat(p)
	if e != nil || !os.SameFile(before, after) {
		t.Fatal("lock inode was replaced")
	}
}
