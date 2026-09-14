package server

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/storage"
	"golang.org/x/crypto/ssh"
	"golang.org/x/net/websocket"
)

// This is a real SSH protocol endpoint with an echo PTY fixture, not an OS shell.
func consoleSSHFixture(t *testing.T, clientKeys ...ssh.PublicKey) (consoleTarget, *atomic.Int32) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	count := &atomic.Int32{}
	cfg := &ssh.ServerConfig{PasswordCallback: func(c ssh.ConnMetadata, p []byte) (*ssh.Permissions, error) {
		count.Add(1)
		if c.User() == "fixture" && string(p) == "fixture-password" {
			return nil, nil
		}
		return nil, io.EOF
	}}
	if len(clientKeys) > 0 {
		cfg.PublicKeyCallback = func(c ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			count.Add(1)
			if c.User() == "fixture" && bytes.Equal(clientKeys[0].Marshal(), key.Marshal()) {
				return nil, nil
			}
			return nil, io.EOF
		}
	}
	cfg.AddHostKey(signer)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, e := ln.Accept()
			if e != nil {
				return
			}
			go func() {
				defer c.Close()
				c.SetDeadline(time.Now().Add(15 * time.Second))
				conn, chans, reqs, e := ssh.NewServerConn(c, cfg)
				if e != nil {
					return
				}
				defer conn.Close()
				go ssh.DiscardRequests(reqs)
				for ch := range chans {
					if ch.ChannelType() != "session" {
						ch.Reject(ssh.UnknownChannelType, "session only")
						continue
					}
					channel, requests, e := ch.Accept()
					if e != nil {
						return
					}
					go func() {
						defer channel.Close()
						for req := range requests {
							switch req.Type {
							case "pty-req":
								req.Reply(true, nil)
							case "window-change":
								var size struct{ Cols, Rows, Width, Height uint32 }
								if ssh.Unmarshal(req.Payload, &size) == nil {
									io.WriteString(channel, fmt.Sprintf("resize:%dx%d\r\n", size.Cols, size.Rows))
								}
								req.Reply(true, nil)
							case "shell":
								req.Reply(true, nil)
								go func() { io.WriteString(channel, "fixture ready\r\n"); io.Copy(channel, channel); channel.Close() }()
							default:
								req.Reply(false, nil)
							}
						}
					}()
				}
			}()
		}
	}()
	host, port, _ := net.SplitHostPort(ln.Addr().String())
	p, _ := strconv.Atoi(port)
	return consoleTarget{Host: host, Port: p, Username: "fixture", Fingerprint: ssh.FingerprintSHA256(signer.PublicKey())}, count
}
func consoleConfig(t *testing.T, a *App, target consoleTarget) {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"targets": map[string]consoleTarget{"h": target}})
	if e := os.WriteFile(filepath.Join(a.Cfg.DataDir, "console-targets.json"), b, 0600); e != nil {
		t.Fatal(e)
	}
}
func consoleLogin(t *testing.T, ts *httptest.Server) (string, string) {
	t.Helper()
	res, e := ts.Client().Post(ts.URL+"/api/v1/login", "application/json", strings.NewReader(`{"username":"owner","password":"supersecret1"}`))
	if e != nil {
		t.Fatal(e)
	}
	defer res.Body.Close()
	var m map[string]any
	json.NewDecoder(res.Body).Decode(&m)
	if res.StatusCode != 200 {
		t.Fatal(res.StatusCode, m)
	}
	return res.Cookies()[0].Value, m["csrf"].(string)
}
func requestConsoleTicket(t *testing.T, ts *httptest.Server, cookie, csrf string) string {
	t.Helper()
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/agents/h/console-ticket", bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	req.AddCookie(&http.Cookie{Name: "monik_session", Value: cookie})
	res, e := ts.Client().Do(req)
	if e != nil {
		t.Fatal(e)
	}
	defer res.Body.Close()
	var m map[string]any
	json.NewDecoder(res.Body).Decode(&m)
	if res.StatusCode != 200 {
		t.Fatal(res.StatusCode, m)
	}
	return m["ticket"].(string)
}
func openConsoleWS(t *testing.T, ts *httptest.Server, cookie, ticket string, custom ...consoleMessage) *websocket.Conn {
	t.Helper()
	cfg, e := websocket.NewConfig("wss"+strings.TrimPrefix(ts.URL, "https")+"/api/v1/agents/h/console-stream", ts.URL)
	if e != nil {
		t.Fatal(e)
	}
	cfg.TlsConfig = ts.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	cfg.Header.Set("Cookie", "monik_session="+cookie)
	ws, e := websocket.DialConfig(cfg)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { ws.Close() })
	ws.SetDeadline(time.Now().Add(10 * time.Second))
	hello := consoleMessage{Type: "authenticate", Ticket: ticket, Password: "fixture-password", Cols: 80, Rows: 24}
	if len(custom) > 0 {
		hello = custom[0]
		hello.Type = "authenticate"
		hello.Ticket = ticket
		hello.Cols = 80
		hello.Rows = 24
	}
	if e = websocket.JSON.Send(ws, hello); e != nil {
		t.Fatal(e)
	}
	return ws
}
func TestAudit8ConsoleSSHPTYInputResizeAndNoTranscript(t *testing.T) {
	target, _ := consoleSSHFixture(t)
	a, h := testApp(t)
	audit7Inventory(t, a)
	consoleConfig(t, a, target)
	ts := httptest.NewTLSServer(h)
	defer ts.Close()
	cookie, csrf := consoleLogin(t, ts)
	ticket := requestConsoleTicket(t, ts, cookie, csrf)
	ws := openConsoleWS(t, ts, cookie, ticket)
	for {
		var m map[string]string
		if e := websocket.JSON.Receive(ws, &m); e != nil {
			t.Fatal(e)
		}
		if m["type"] == "ready" {
			break
		}
		if m["type"] == "error" {
			t.Fatal(m)
		}
	}
	if e := websocket.JSON.Send(ws, consoleMessage{Type: "resize", Cols: 100, Rows: 30}); e != nil {
		t.Fatal(e)
	}
	if e := websocket.JSON.Send(ws, consoleMessage{Type: "input", Data: "private-fixture-input\r"}); e != nil {
		t.Fatal(e)
	}
	found, resized := false, false
	output := ""
	for i := 0; i < 10 && (!found || !resized); i++ {
		var m map[string]string
		if e := websocket.JSON.Receive(ws, &m); e != nil {
			t.Fatal(e)
		}
		if m["type"] == "output" {
			b, _ := base64.StdEncoding.DecodeString(m["data"])
			output += string(b)
			found = strings.Contains(output, "private-fixture-input")
			resized = strings.Contains(output, "resize:100x30")
		}
	}
	if !found || !resized {
		t.Fatal("SSH echo/resize did not reach browser transport")
	}
	websocket.JSON.Send(ws, consoleMessage{Type: "close"})
	ws.Close()
	var count int
	if e := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE detail LIKE '%private-fixture%' OR detail LIKE '%fixture-password%'`).Scan(&count); e != nil || count != 0 {
		t.Fatal(count, e)
	}
}
func TestAudit8ConsoleWrongKeyNeverAuthenticates(t *testing.T) {
	target, count := consoleSSHFixture(t)
	target.Fingerprint = "SHA256:" + base64.RawStdEncoding.EncodeToString(make([]byte, 32))
	a, h := testApp(t)
	audit7Inventory(t, a)
	consoleConfig(t, a, target)
	ts := httptest.NewTLSServer(h)
	defer ts.Close()
	cookie, csrf := consoleLogin(t, ts)
	ws := openConsoleWS(t, ts, cookie, requestConsoleTicket(t, ts, cookie, csrf))
	var m map[string]string
	if e := websocket.JSON.Receive(ws, &m); e != nil {
		t.Fatal(e)
	}
	if m["type"] != "error" || !strings.Contains(m["message"], "Ключ") {
		t.Fatal(m)
	}
	if count.Load() != 0 {
		t.Fatal("credentials sent before host authentication")
	}
}
func TestAudit8ConsoleTicketBoundSingleUseAndExpired(t *testing.T) {
	a, _ := testApp(t)
	target := consoleTarget{Host: "127.0.0.1", Port: 22, Username: "fixture", Fingerprint: "SHA256:" + base64.RawStdEncoding.EncodeToString(make([]byte, 32))}
	s := &storage.Session{ID: "session"}
	put := func(exp time.Time) {
		a.console.tickets = map[string]consoleTicket{"token": {Session: s.ID, Agent: "h", Target: targetIdentity(target), Expires: exp}}
	}
	put(a.Clock.Now().Add(time.Second))
	if a.takeConsoleTicket("token", s, "different", target) {
		t.Fatal("wrong machine accepted")
	}
	put(a.Clock.Now().Add(time.Second))
	if !a.takeConsoleTicket("token", s, "h", target) || a.takeConsoleTicket("token", s, "h", target) {
		t.Fatal("single use failed")
	}
	put(a.Clock.Now().Add(-time.Second))
	if a.takeConsoleTicket("token", s, "h", target) {
		t.Fatal("expired ticket")
	}
}
func TestAudit8ConsoleOriginAndDestinationPolicy(t *testing.T) {
	req := httptest.NewRequest("GET", "https://localhost/socket", nil)
	req.TLS = &tls.ConnectionState{}
	for _, v := range []string{"", "null", "http://localhost", "https://evil.test", "https://localhost.evil.test"} {
		req.Header.Set("Origin", v)
		if consoleOrigin(req) {
			t.Fatal(v)
		}
	}
	req.Header.Set("Origin", "https://localhost")
	if !consoleOrigin(req) {
		t.Fatal("same origin denied")
	}
	target := consoleTarget{Port: 22, Username: "fixture", Fingerprint: "SHA256:" + base64.RawStdEncoding.EncodeToString(make([]byte, 32))}
	for _, host := range []string{"example.test", "0.0.0.0", "169.254.169.254", "224.0.0.1", "100.100.100.200", "fe80::1"} {
		target.Host = host
		if validateConsoleTarget(target) == nil {
			t.Fatal(host)
		}
	}
	for _, host := range []string{"127.0.0.1", "192.168.1.2", "::1"} {
		target.Host = host
		if e := validateConsoleTarget(target); e != nil {
			t.Fatal(host, e)
		}
	}
}
func TestAudit8ConsoleSessionRevocation(t *testing.T) {
	target, _ := consoleSSHFixture(t)
	a, h := testApp(t)
	audit7Inventory(t, a)
	consoleConfig(t, a, target)
	ts := httptest.NewTLSServer(h)
	defer ts.Close()
	cookie, csrf := consoleLogin(t, ts)
	ws := openConsoleWS(t, ts, cookie, requestConsoleTicket(t, ts, cookie, csrf))
	for {
		var m map[string]string
		if e := websocket.JSON.Receive(ws, &m); e != nil {
			t.Fatal(e)
		}
		if m["type"] == "ready" {
			break
		}
	}
	sess, e := a.Store.SessionByToken(cookie)
	if e != nil {
		t.Fatal(e)
	}
	if e = a.Store.DeleteSession(sess.ID); e != nil {
		t.Fatal(e)
	}
	for {
		var m map[string]string
		if e := websocket.JSON.Receive(ws, &m); e != nil {
			t.Fatal("expected explicit revoke before timeout", e)
		}
		if m["type"] == "error" {
			if !strings.Contains(m["message"], "отозван") {
				t.Fatal(m)
			}
			break
		}
	}
}

func TestAudit8ConsoleOutputIsBounded(t *testing.T) {
	seen := []string{}
	w := &consoleOutput{limit: 12, send: func(m any) error { seen = append(seen, m.(map[string]string)["type"]); return nil }}
	if n, e := w.Write([]byte("12345678")); e != nil || n != 8 {
		t.Fatal(n, e)
	}
	if n, e := w.Write([]byte("abcdef")); e == nil || n != 0 {
		t.Fatal("unbounded output", n, e)
	}
	if len(seen) != 2 || seen[0] != "output" || seen[1] != "error" {
		t.Fatal(seen)
	}
}
func TestAudit8ConsoleRequiresOwnerCSRFAndKnownMachine(t *testing.T) {
	a, h := testApp(t)
	audit7Inventory(t, a)
	target, _ := consoleSSHFixture(t)
	consoleConfig(t, a, target)
	ts := httptest.NewTLSServer(h)
	defer ts.Close()
	res, e := ts.Client().Get(ts.URL + "/api/v1/agents/h/console")
	if e != nil {
		t.Fatal(e)
	}
	res.Body.Close()
	if res.StatusCode != 401 {
		t.Fatal(res.StatusCode)
	}
	cookie, _ := consoleLogin(t, ts)
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/agents/h/console-ticket", nil)
	req.AddCookie(&http.Cookie{Name: "monik_session", Value: cookie})
	res, e = ts.Client().Do(req)
	if e != nil {
		t.Fatal(e)
	}
	res.Body.Close()
	if res.StatusCode != 403 {
		t.Fatal("missing CSRF accepted")
	}
	w := httptest.NewRecorder()
	a.handleConsoleInfo(w, httptest.NewRequest("GET", "https://localhost/api/v1/agents/h/console", nil), &storage.Session{Role: "viewer"})
	if w.Code != 403 {
		t.Fatal("viewer got console")
	}
	a.Cfg.RestoreMode = true
	if _, e := a.consoleTarget("h"); e == nil {
		t.Fatal("restored controller enabled remote console")
	}
}

func TestAudit8ConsoleEncryptedKeyAuthentication(t *testing.T) {
	_, priv, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	pub, e := ssh.NewPublicKey(priv.Public())
	if e != nil {
		t.Fatal(e)
	}
	block, e := ssh.MarshalPrivateKeyWithPassphrase(priv, "fixture", []byte("fixture-key-passphrase"))
	if e != nil {
		t.Fatal(e)
	}
	target, count := consoleSSHFixture(t, pub)
	a, h := testApp(t)
	audit7Inventory(t, a)
	consoleConfig(t, a, target)
	ts := httptest.NewTLSServer(h)
	defer ts.Close()
	cookie, csrf := consoleLogin(t, ts)
	ws := openConsoleWS(t, ts, cookie, requestConsoleTicket(t, ts, cookie, csrf), consoleMessage{PrivateKey: string(pem.EncodeToMemory(block)), Passphrase: "fixture-key-passphrase"})
	for {
		var m map[string]string
		if e = websocket.JSON.Receive(ws, &m); e != nil {
			t.Fatal(e)
		}
		if m["type"] == "error" {
			t.Fatal(m["message"])
		}
		if m["type"] == "ready" {
			break
		}
	}
	if count.Load() == 0 {
		t.Fatal("key authentication not reached")
	}
}
