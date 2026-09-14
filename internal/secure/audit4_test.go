package secure

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAudit4MalformedNonceReturnsError(t *testing.T) {
	defer func() {
		if v := recover(); v != nil {
			t.Fatalf("malformed nonce panicked: %v", v)
		}
	}()
	if _, err := Open(make([]byte, 32), []byte{1}, make([]byte, 32)); err == nil {
		t.Fatal("accepted invalid nonce")
	}
}
func TestAudit4CorruptKeyNeverReplaced(t *testing.T) {
	p := filepath.Join(t.TempDir(), "key")
	original := []byte("corrupt")
	if err := os.WriteFile(p, original, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateKey(p, 32); err == nil {
		t.Error("accepted a corrupt key")
	}
	after, _ := os.ReadFile(p)
	if string(after) != string(original) {
		t.Fatal("destroyed existing key")
	}
}
func TestAudit4MalformedPasswordHashDoesNotPanic(t *testing.T) {
	defer func() {
		if v := recover(); v != nil {
			t.Fatalf("malformed password hash panicked: %v", v)
		}
	}()
	if VerifyPassword("argon2id$v=19$m=65536,t=0,p=0$0000$0000", "password") {
		t.Fatal("invalid hash accepted")
	}
}
