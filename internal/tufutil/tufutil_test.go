package tufutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSignVerifyPackImport(t *testing.T) {
	keysDir := t.TempDir()
	repo := t.TempDir()
	ks, err := InitKeys(keysDir)
	if err != nil {
		t.Fatal(err)
	}
	art := filepath.Join(t.TempDir(), "linux-amd64-monik-agent")
	if err := os.WriteFile(art, []byte("agent-bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := SignRepository(ks, repo, map[string]string{"linux-amd64/monik-agent": art}, 30, 2); err != nil {
		t.Fatal(err)
	}
	if _, _, err := VerifyLocal(repo, "linux-amd64/monik-agent"); err != nil {
		t.Fatal(err)
	}
	bundle := filepath.Join(t.TempDir(), "rel.tgz")
	if err := PackBundle(repo, bundle, "0.1.0", "test"); err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	res, err := ImportBundle(bundle, dest)
	if err != nil {
		t.Fatal(err)
	}
	if res.Version != "0.1.0" || len(res.Artifacts) != 1 {
		t.Fatalf("%+v", res)
	}
}
