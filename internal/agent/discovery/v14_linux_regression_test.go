//go:build linux

package discovery

import (
	"os"
	"path/filepath"
	"testing"
)

func TestV14RegressMalformedProcListenerIsNotCompleteEmptyInventory(t *testing.T) {
	p := filepath.Join(t.TempDir(), "tcp")
	if e := os.WriteFile(p, []byte("sl local_address rem_address st tx_queue rx_queue\n 0: GG00007F:1F90 00000000:0000 0A 0 0 0 0 0 999\n"), 0600); e != nil {
		t.Fatal(e)
	}
	if rows, e := parseProcNet(p); e == nil {
		t.Fatalf("malformed listener was silently dropped: %+v", rows)
	}
}

func TestV14InvalidProcHeaderCannotConfirmEmptyInventory(t *testing.T) {
	p := filepath.Join(t.TempDir(), "tcp")
	if e := os.WriteFile(p, []byte("not a tcp table\n"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := parseProcNet(p); e == nil {
		t.Fatal("invalid table reported complete")
	}
}
