package tufutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAudit10CorruptSigningKeyIsNotReplaced(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "root.ed25519")
	if e := os.WriteFile(p, []byte("broken"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := InitKeys(d); e == nil {
		t.Error("corrupt signing key silently replaced")
	}
	b, _ := os.ReadFile(p)
	if string(b) != "broken" {
		t.Error("original key was overwritten")
	}
}
func TestAudit10NullHighWaterIsRejected(t *testing.T) {
	d := t.TempDir()
	os.MkdirAll(filepath.Dir(HighWaterPath(d)), 0700)
	os.WriteFile(HighWaterPath(d), []byte("null"), 0600)
	if _, e := ReadHighWater(d); e == nil {
		t.Fatal("null resets anti-rollback state")
	}
}
