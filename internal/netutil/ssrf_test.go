package netutil

import (
	"net"
	"testing"
)

func TestParseURLRejects(t *testing.T) {
	if _, err := ParseURL("http://user:pass@127.0.0.1/"); err == nil {
		t.Fatal("userinfo")
	}
	if _, err := ParseURL("ftp://127.0.0.1/"); err == nil {
		t.Fatal("scheme")
	}
	if _, err := ParseURL("http://127.0.0.1/\nHost: evil"); err == nil {
		t.Fatal("crlf")
	}
}

func TestValidateControllerURL(t *testing.T) {
	if _, err := ValidateControllerURL("http://192.0.2.8:8777"); err == nil {
		t.Fatal("http")
	}
	if _, err := ValidateControllerURL("https://169.254.169.254/"); err == nil {
		t.Fatal("metadata");
	}
	if _, err := ValidateControllerURL("https://192.0.2.8:8777"); err != nil {
		t.Fatal(err)
	}
}

func TestAllowedDial(t *testing.T) {
	locals := []net.IP{net.ParseIP("192.168.12.128")}
	p := DefaultPolicy()
	if _, err := AllowedDial("127.0.0.1:80", p, locals); err != nil {
		t.Fatal(err)
	}
	if _, err := AllowedDial("192.168.12.128:8777", p, locals); err != nil {
		t.Fatal(err)
	}
	if _, err := AllowedDial("8.8.8.8:80", p, locals); err == nil {
		t.Fatal("remote should be denied")
	}
	if _, err := AllowedDial("169.254.169.254:80", p, locals); err == nil {
		t.Fatal("metadata")
	}
	if _, err := AllowedDial("0.0.0.0:80", p, locals); err == nil {
		t.Fatal("wildcard")
	}
	if _, err := AllowedDial("example.com:80", p, locals); err == nil {
		t.Fatal("dns")
	}
}
