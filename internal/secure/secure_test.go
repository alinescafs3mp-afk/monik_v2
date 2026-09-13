package secure

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPasswordRoundTrip(t *testing.T) {
	h, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword(h, "correct horse battery staple") {
		t.Fatal("expected match")
	}
	if VerifyPassword(h, "wrong") {
		t.Fatal("expected mismatch")
	}
}

func TestSealOpen(t *testing.T) {
	dir := t.TempDir()
	key, err := LoadOrCreateKey(filepath.Join(dir, "m.key"), 32)
	if err != nil {
		t.Fatal(err)
	}
	n, ct, err := Seal(key, []byte("secret-value"))
	if err != nil {
		t.Fatal(err)
	}
	pt, err := Open(key, n, ct)
	if err != nil {
		t.Fatal(err)
	}
	if string(pt) != "secret-value" {
		t.Fatalf("got %s", pt)
	}
}

func TestSHA256File(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(p, []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	sum, n, err := SHA256File(p)
	if err != nil || n != 3 || len(sum) != 64 {
		t.Fatalf("%s %d %v", sum, n, err)
	}
}

