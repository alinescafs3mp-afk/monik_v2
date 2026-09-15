package agentconsole

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"
)

func TestV18StrictFramesAndSequence(t *testing.T) {
	bad := []string{`null`, `[]`, `{"type":"input","type":"close"}`, `{"type":"input","Type":"close"}`, `{"type":"input","data":null}`, `{"type":"open","command":"id"}`, `{"type":"open","user":"root","cwd":"/"}`, `{"type":"input","seq":1.2}`, `{"type":"input","seq":-1}`, `{"type":"input"} {}`, strings.Repeat(" ", MaxFrame+1)}
	for _, raw := range bad {
		if _, e := Decode([]byte(raw)); e == nil {
			t.Errorf("accepted %q", raw[:min(len(raw), 100)])
		}
	}
	good := Message{Type: "input", Session: strings.Repeat("a", 64), Data: "echo Привет\r", Seq: 1}
	raw, _ := json.Marshal(good)
	m, e := Decode(raw)
	if e != nil || !ValidateInput(m, 0) || ValidateInput(m, 1) {
		t.Fatal(m, e)
	}
	for _, m := range []Message{{Type: "input", Data: strings.Repeat("x", 4097), Seq: 1}, {Type: "input", Data: "x", Ticket: "secret", Seq: 1}, {Type: "resize", Cols: 900, Rows: 24, Seq: 1}, {Type: "close", Data: "cmd", Seq: 1}} {
		if ValidateInput(m, 0) {
			t.Fatal("unsafe input accepted", m.Type)
		}
	}
}
func TestV18LocalFramingBoundedAndComplete(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	l := NewLocal(a)
	done := make(chan error, 1)
	go func() { _, e := b.Write([]byte(strings.Repeat(" ", MaxFrame+2))); done <- e }()
	if _, e := l.Read(); e == nil {
		t.Fatal("oversized local frame accepted")
	}
	a.Close()
	<-done
	a, b = net.Pipe()
	defer a.Close()
	defer b.Close()
	l = NewLocal(a)
	go func() { _, e := b.Write([]byte(`{"type":"info"}` + "\n")); done <- e }()
	if m, e := l.Read(); e != nil || m.Type != "info" {
		t.Fatal(m, e)
	}
	<-done
}
func TestV18DialNeverAcceptsInsecureOrArbitraryDestinations(t *testing.T) {
	for _, b := range []Binding{{URL: "http://127.0.0.1"}, {URL: "https://u:p@127.0.0.1"}, {URL: "https://127.0.0.1/x"}, {URL: "https://127.0.0.1/?secret=1"}, {URL: "https://127.0.0.1", TLS: &tls.Config{InsecureSkipVerify: true}}} {
		ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
		_, e := Dial(ctx, b)
		cancel()
		if e == nil {
			t.Fatal("insecure binding accepted")
		}
	}
}
func TestV18OutputCannotCarryAuthority(t *testing.T) {
	if _, e := OutputBytes(Message{Type: "output", Data: "aGk=", Ticket: "x"}); e == nil {
		t.Fatal("mixed authority allowed")
	}
	b, e := OutputBytes(Message{Type: "output", Data: "aGk="})
	if e != nil || !bytes.Equal(b, []byte("hi")) {
		t.Fatal(e)
	}
}
func FuzzV18TerminalFrames(f *testing.F) {
	for _, s := range []string{`{"type":"input","data":"a","seq":1}`, `null`, `{"type":"open","command":"ls"}`} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		m, e := Decode(b)
		if e == nil {
			raw, e := json.Marshal(m)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = Decode(raw); e != nil {
				t.Fatal(e)
			}
			if ValidateInput(m, 0) && m.Seq != 1 {
				t.Fatal("replay accepted")
			}
		}
	})
}

// Oversize and arbitrary commands cannot be smuggled into ostensibly harmless fields.
func TestV18InputUsesByteLimitsNotCharacterCounts(t *testing.T) {
	m := Message{Type: "input", Data: strings.Repeat("я", 2049), Seq: 1}
	if ValidateInput(m, 0) {
		t.Fatal("UTF-8 exceeded 4096 byte bound")
	}
	m.Data = strings.Repeat("я", 2048)
	if !ValidateInput(m, 0) {
		t.Fatal("exact bound rejected")
	}
}
