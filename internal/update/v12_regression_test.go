package update

import (
	"os"
	"testing"
)

func TestV12UnknownOrNullUpdateJournalRefusesRecovery(t *testing.T) {
	for _, text := range []string{"null", "{}", `{"stage":"unrecognized"}`} {
		t.Run(text, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(Path(dir), []byte(text), 0600); err != nil {
				t.Fatal(err)
			}
			if j, err := Load(dir); err == nil {
				t.Fatalf("invalid activation state silently accepted: %+v", j)
			}
			got, _ := os.ReadFile(Path(dir))
			if string(got) != text {
				t.Fatal("journal overwritten")
			}
		})
	}
}
