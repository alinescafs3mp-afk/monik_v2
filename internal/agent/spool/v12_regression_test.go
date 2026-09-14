package spool

import (
	"os"
	"path/filepath"
	"testing"
)

func TestV12InvalidJSONReportCannotBlockValidSpoolDrain(t *testing.T) {
	for _, text := range []string{"null", "{}", `{"agent_id":"x"}`} {
		t.Run(text, func(t *testing.T) {
			dir := t.TempDir()
			s, err := Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(dir, "000-invalid.json"), []byte(text), 0600); err != nil {
				t.Fatal(err)
			}
			valid := report(7)
			if err = s.Push(valid); err != nil {
				t.Fatal(err)
			}
			rows, err := s.ListLimit(4)
			if err == nil || len(rows) != 1 || rows[0].Sequence != 7 {
				t.Fatalf("corrupt entry reached sender or hid valid report: %+v err=%v", rows, err)
			}
			if _, err = os.Stat(filepath.Join(dir, "000-invalid.json")); err != nil {
				t.Fatal("corrupt evidence was deleted")
			}
		})
	}
}
