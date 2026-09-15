package netutil

import "testing"

func TestV14CanonicalListenerIdentity(t *testing.T) {
	for raw, want := range map[string]string{"127.0.0.1:080": "127.0.0.1:80", "[0:0:0:0:0:0:0:1]:8080": "[::1]:8080", "[::ffff:127.0.0.1]:80": "127.0.0.1:80", "[fe80::123%7]:8080": "[fe80::123%7]:8080"} {
		got, e := CanonicalListenerTarget(raw)
		if e != nil || got != want {
			t.Fatal(raw, got, want, e)
		}
	}
	for _, bad := range []string{"localhost:80", "0.0.0.0:80", "[::]:80", "127.0.0.1:0", "127.0.0.1:70000", "bad", "[ff02::1]:80"} {
		if _, e := CanonicalListenerTarget(bad); e == nil {
			t.Fatal("accepted", bad)
		}
	}
}
