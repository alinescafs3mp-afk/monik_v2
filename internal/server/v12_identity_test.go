package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestV12ExistingControllerCannotRegenerateMissingIdentity(t *testing.T) {
	a, _ := testApp(t)
	if _, err := a.Store.DB.Exec(`DELETE FROM settings WHERE key='controller_id'`); err != nil {
		t.Fatal(err)
	}
	cfg := a.Cfg
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	b, err := Open(cfg)
	if err == nil {
		b.Close()
		t.Fatal("existing controller generated a new identity after state loss")
	}
}
func TestV12EstablishedControllerCannotRegenerateLostCA(t *testing.T) {
	a, _ := testApp(t)
	if err := os.RemoveAll(filepath.Join(a.Cfg.DataDir, "tls")); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := a.Run(ctx); err == nil {
		t.Fatal("missing CA was silently regenerated")
	}
	if _, err := os.Stat(filepath.Join(a.Cfg.DataDir, "tls", "ca.key")); !os.IsNotExist(err) {
		t.Fatal("lost enrolled trust was replaced")
	}
}
func TestV12DatabaseLossIsNotFreshControllerSetup(t *testing.T) {
	a, _ := testApp(t)
	cfg := a.Cfg
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(cfg.DataDir, "monik.db")); err != nil {
		t.Fatal(err)
	}
	b, err := Open(cfg)
	if err == nil {
		b.Close()
		t.Fatal("missing database silently became a different empty controller")
	}
}
