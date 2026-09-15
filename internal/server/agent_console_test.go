package server

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	ac "github.com/alinescafs3mp-afk/monik_v2/internal/agentconsole"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"github.com/alinescafs3mp-afk/monik_v2/internal/secure"
	"github.com/alinescafs3mp-afk/monik_v2/internal/storage"
	"golang.org/x/net/websocket"
)

type v18Fixture struct {
	a            *App
	ts           *httptest.Server
	cookie, csrf string
	binding      ac.Binding
	cancel       context.CancelFunc
	done         chan error
	opens        atomic.Int32
	inputs       chan ac.Message
	localWG      sync.WaitGroup
}

func v18New(t *testing.T) *v18Fixture {
	t.Helper()
	a, h := testApp(t)
	cfg := protocol.DefaultAgentConfig()
	raw, _ := json.Marshal(cfg)
	cred := strings.Repeat("9", 64)
	if e := a.Store.InsertAgent(&storage.AgentRow{ID: "h", Hostname: "fixture", DesiredRevision: 1, DesiredConfig: string(raw), DesiredHash: secure.SHA256Bytes(raw)}, secure.HashToken(cred)); e != nil {
		t.Fatal(e)
	}
	if e := a.Store.TouchAgent("h", "worker-h", 1, true, nil, nil, map[string]string{}); e != nil {
		t.Fatal(e)
	}
	ts := httptest.NewTLSServer(h)
	roots := x509.NewCertPool()
	roots.AddCert(ts.Certificate())
	f := &v18Fixture{a: a, ts: ts, inputs: make(chan ac.Message, 20), binding: ac.Binding{URL: ts.URL, AgentID: "h", Credential: cred, WorkerSession: "worker-h", ControllerID: a.ControllerID(), TLS: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}}
	f.cookie, f.csrf = consoleLogin(t, ts)
	t.Cleanup(func() {
		if f.cancel != nil {
			f.cancel()
			select {
			case <-f.done:
			case <-time.After(6 * time.Second):
				t.Error("link did not stop")
			}
		}
		a.agentConsole.closeAll()
		ts.Close()
		f.localWG.Wait()
	})
	return f
}
func (f *v18Fixture) request(t *testing.T, method, path string, body any, cookie, csrf string) (int, map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(body)
	r, _ := http.NewRequest(method, f.ts.URL+path, bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	if cookie != "" {
		r.AddCookie(&http.Cookie{Name: "monik_session", Value: cookie})
	}
	r.Header.Set("X-CSRF-Token", csrf)
	res, e := f.ts.Client().Do(r)
	if e != nil {
		t.Fatal(e)
	}
	defer res.Body.Close()
	m := map[string]any{}
	json.NewDecoder(res.Body).Decode(&m)
	return res.StatusCode, m
}
func (f *v18Fixture) start(t *testing.T) {
	f.startWith(t, nil)
}
func (f *v18Fixture) startWith(t *testing.T, realDial func(context.Context) (net.Conn, error)) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	f.cancel = cancel
	f.done = make(chan error, 1)
	s, e := ac.Dial(ctx, f.binding)
	if e != nil {
		t.Fatal(e)
	}
	dial := func(ctx context.Context) (net.Conn, error) {
		a, b := net.Pipe()
		f.localWG.Add(1)
		go func() {
			defer f.localWG.Done()
			defer b.Close()
			l := ac.NewLocal(b)
			m, e := l.Read()
			if e != nil {
				return
			}
			f.opens.Add(1)
			if l.Send(ac.Message{Type: "ready", Session: m.Session, User: "monik-console"}) != nil {
				return
			}
			for {
				in, e := l.Read()
				if e != nil {
					return
				}
				select {
				case f.inputs <- in:
				default:
				}
				if in.Type == "input" {
					if l.Send(ac.Message{Type: "output", Session: m.Session, Data: base64.StdEncoding.EncodeToString([]byte(in.Data))}) != nil {
						return
					}
				}
			}
		}()
		return a, nil
	}
	if realDial != nil {
		dial = realDial
	}
	go func() { f.done <- ac.RunLink(ctx, s, f.binding.ControllerID, "monik-console", dial) }()
	until := time.Now().Add(3 * time.Second)
	for time.Now().Before(until) {
		code, info := f.request(t, "GET", "/api/v1/agents/h/agent-console", nil, f.cookie, f.csrf)
		if code == 200 && info["enabled"] == true {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("channel not ready")
}
func (f *v18Fixture) ticket(t *testing.T) string {
	t.Helper()
	_, info := f.request(t, "GET", "/api/v1/agents/h/agent-console", nil, f.cookie, f.csrf)
	status, m := f.request(t, "POST", "/api/v1/agents/h/agent-console-ticket", map[string]any{"target_revision": info["channel_revision"]}, f.cookie, f.csrf)
	if status != 200 {
		t.Fatal(status, m)
	}
	return m["ticket"].(string)
}
func (f *v18Fixture) browser(t *testing.T, path, cookie, origin string) (*websocket.Conn, error) {
	t.Helper()
	c, e := websocket.NewConfig("wss"+strings.TrimPrefix(f.ts.URL, "https")+path, origin)
	if e != nil {
		return nil, e
	}
	c.TlsConfig = f.binding.TLS.Clone()
	c.Header.Set("Cookie", "monik_session="+cookie)
	c.Dialer = &net.Dialer{Timeout: 2 * time.Second}
	return websocket.DialConfig(c)
}
func (f *v18Fixture) open(t *testing.T, ticket string) *ac.Socket {
	t.Helper()
	w, e := f.browser(t, "/api/v1/agents/h/agent-console-stream", f.cookie, f.ts.URL)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { w.Close() })
	s := &ac.Socket{WS: w}
	w.SetDeadline(time.Now().Add(5 * time.Second))
	if e = s.Send(ac.Message{Type: "authenticate", Ticket: ticket, Cols: 80, Rows: 24}); e != nil {
		t.Fatal(e)
	}
	return s
}
func v18Ready(t *testing.T, s *ac.Socket) {
	t.Helper()
	m, e := s.Read()
	if e != nil || m.Type != "ready" {
		t.Fatal("ready", m, e)
	}
}
func TestV18OutboundChannelIsNotPermissionToOpenShell(t *testing.T) {
	f := v18New(t)
	code, _ := f.request(t, "GET", "/api/v1/agents/h/agent-console", nil, f.cookie, f.csrf)
	if code != 200 {
		t.Fatal(code)
	}
	f.start(t)
	if f.opens.Load() != 0 {
		t.Fatal("connecting agent created shell")
	}
	s := f.open(t, f.ticket(t))
	v18Ready(t, s)
	if f.opens.Load() != 1 {
		t.Fatal("not exactly one shell")
	}
	if e := s.Send(ac.Message{Type: "input", Seq: 1, Data: "private-fixture-input\r"}); e != nil {
		t.Fatal(e)
	}
	m, e := s.Read()
	b, _ := ac.OutputBytes(m)
	if e != nil || string(b) != "private-fixture-input\r" {
		t.Fatal(m, e)
	}
	var n int
	if e = f.a.Store.DB.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE detail LIKE '%private-fixture-input%'`).Scan(&n); e != nil || n != 0 {
		t.Fatal("transcript leaked", n, e)
	}
	if e = f.a.Store.DB.QueryRow(`SELECT COUNT(*) FROM agent_jobs`).Scan(&n); e != nil || n != 0 {
		t.Fatal("terminal became a durable job", n, e)
	}
	if e = s.Send(ac.Message{Type: "resize", Seq: 2, Cols: 100, Rows: 32}); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 2; i++ {
		select {
		case m = <-f.inputs:
			if i == 1 && (m.Type != "resize" || m.Cols != 100 || m.Rows != 32) {
				t.Fatal(m)
			}
		case <-time.After(time.Second):
			t.Fatal("missing input")
		}
	}
}
func TestV18NetworkAuthenticationAndCSRFBoundaries(t *testing.T) {
	f := v18New(t)
	f.start(t)
	_, info := f.request(t, "GET", "/api/v1/agents/h/agent-console", nil, f.cookie, f.csrf)
	body := map[string]any{"target_revision": info["channel_revision"]}
	for _, c := range []struct {
		name, cookie, csrf string
		body               any
		want               int
	}{{"anonymous", "", "", body, 401}, {"csrf", f.cookie, "", body, 403}, {"old-channel", f.cookie, f.csrf, map[string]any{"target_revision": strings.Repeat("a", 64)}, 409}, {"injected-command", f.cookie, f.csrf, map[string]any{"target_revision": info["channel_revision"], "command": "uname"}, 400}} {
		t.Run(c.name, func(t *testing.T) {
			status, _ := f.request(t, "POST", "/api/v1/agents/h/agent-console-ticket", c.body, c.cookie, c.csrf)
			if status != c.want {
				t.Fatal(status)
			}
		})
	}
	for _, origin := range []string{"https://evil.example", f.ts.URL + ".evil", "null"} {
		w, e := f.browser(t, "/api/v1/agents/h/agent-console-stream", f.cookie, origin)
		if e == nil {
			w.Close()
			t.Fatal("foreign origin accepted", origin)
		}
	}
	w, e := f.browser(t, "/api/v1/agents/h/agent-console-stream?ticket=secret", f.cookie, f.ts.URL)
	if e == nil {
		w.Close()
		t.Fatal("URL credential allowed")
	}
	if f.opens.Load() != 0 {
		t.Fatal("negative test opened shell")
	}
}
func TestV18TicketReplayAndWrongMachineCannotOpenAnotherShell(t *testing.T) {
	f := v18New(t)
	f.start(t)
	ticket := f.ticket(t)
	s := f.open(t, ticket)
	v18Ready(t, s)
	replay := f.open(t, ticket)
	if m, e := replay.Read(); e == nil {
		t.Fatal("replay accepted", m)
	}
	if f.opens.Load() != 1 {
		t.Fatal("replayed shell")
	}
	if e := f.a.Store.InsertAgent(&storage.AgentRow{ID: "other", DesiredConfig: `{}`}, secure.HashToken(strings.Repeat("8", 64))); e != nil {
		t.Fatal(e)
	}
	w, e := f.browser(t, "/api/v1/agents/other/agent-console-stream", f.cookie, f.ts.URL)
	if e == nil {
		defer w.Close()
		ws := &ac.Socket{WS: w}
		w.SetDeadline(time.Now().Add(3 * time.Second))
		ws.Send(ac.Message{Type: "authenticate", Ticket: ticket, Cols: 80, Rows: 24})
		if _, e = ws.Read(); e == nil {
			t.Fatal("cross-machine ticket accepted")
		}
	}
}
func TestV18AuthRevocationClosesLiveChannelAndNoReplay(t *testing.T) {
	for _, what := range []string{"session", "role", "agent", "credential", "worker"} {
		t.Run(what, func(t *testing.T) {
			f := v18New(t)
			f.start(t)
			s := f.open(t, f.ticket(t))
			v18Ready(t, s)
			var e error
			switch what {
			case "role":
				_, e = f.a.Store.DB.Exec(`UPDATE admin_users SET role='viewer'`)
			case "session":
				_, e = f.a.Store.DB.Exec(`DELETE FROM admin_sessions`)
			case "agent":
				_, e = f.a.Store.DB.Exec(`UPDATE agents SET revoked=1 WHERE id='h'`)
			case "credential":
				_, e = f.a.Store.DB.Exec(`UPDATE agents SET credential_hash=? WHERE id='h'`, secure.HashToken(strings.Repeat("b", 64)))
			case "worker":
				_, e = f.a.Store.DB.Exec(`UPDATE agents SET session_id='replacement' WHERE id='h'`)
			}
			if e != nil {
				t.Fatal(e)
			}
			s.WS.SetReadDeadline(time.Now().Add(4 * time.Second))
			if m, e := s.Read(); e == nil {
				t.Fatal("revoked session stayed usable", m)
			}
			if f.opens.Load() != 1 {
				t.Fatal("automatic reopen")
			}
		})
	}
}
func TestV18MalformedAndRepeatedInputCloseWithoutDispatch(t *testing.T) {
	for _, frame := range []string{`{"type":"input","seq":1,"seq":2,"data":"x"}`, `{"type":"input","seq":1,"data":"x","target":"other"}`, `{"type":"input","seq":2,"data":"x"}`, `{"type":"resize","seq":1,"cols":99999,"rows":24}`, strings.Repeat(" ", ac.MaxFrame+1)} {
		t.Run(fmt.Sprint(len(frame), frame[:min(15, len(frame))]), func(t *testing.T) {
			f := v18New(t)
			f.start(t)
			s := f.open(t, f.ticket(t))
			v18Ready(t, s)
			websocket.Message.Send(s.WS, frame)
			if m, e := s.Read(); e == nil {
				t.Fatal("invalid input accepted", m)
			}
			select {
			case m := <-f.inputs:
				t.Fatal("invalid command routed", m)
			default:
			}
		})
	}
}
func TestV18AuditWriteFailurePreventsOpeningLocalTerminal(t *testing.T) {
	f := v18New(t)
	f.start(t)
	ticket := f.ticket(t)
	if _, e := f.a.Store.DB.Exec(`CREATE TRIGGER deny_console BEFORE INSERT ON audit_events WHEN NEW.action='agent_console.open' BEGIN SELECT RAISE(FAIL,'fixture'); END`); e != nil {
		t.Fatal(e)
	}
	s := f.open(t, ticket)
	if _, e := s.Read(); e == nil {
		t.Fatal("auditless shell")
	}
	if f.opens.Load() != 0 {
		t.Fatal("audit failure still opened shell")
	}
}
func TestV18EncryptedAgentRejectsWrongTLSAndController(t *testing.T) {
	f := v18New(t)
	bad := f.binding
	bad.TLS = &tls.Config{RootCAs: x509.NewCertPool(), MinVersion: tls.VersionTLS12}
	if s, e := ac.Dial(context.Background(), bad); e == nil {
		s.Close()
		t.Fatal("untrusted controller accepted")
	}
	bad = f.binding
	bad.WorkerSession = "old"
	if s, e := ac.Dial(context.Background(), bad); e == nil {
		s.Close()
		t.Fatal("stale worker accepted")
	}
	bad = f.binding
	bad.AgentID = "other"
	if s, e := ac.Dial(context.Background(), bad); e == nil {
		s.Close()
		t.Fatal("other machine accepted")
	}
	s, e := ac.Dial(context.Background(), f.binding)
	if e != nil {
		t.Fatal(e)
	}
	e = ac.RunLink(context.Background(), s, "wrong-controller", "monik-console", func(context.Context) (net.Conn, error) { t.Error("dial after identity mismatch"); return nil, io.EOF })
	if e == nil {
		t.Fatal("wrong identity allowed")
	}
}
func TestV18ShutdownIncludesPreauthBrowserAndAgent(t *testing.T) {
	f := v18New(t)
	f.start(t)
	w, e := f.browser(t, "/api/v1/agents/h/agent-console-stream", f.cookie, f.ts.URL)
	if e != nil {
		t.Fatal(e)
	}
	defer w.Close()
	time.Sleep(10 * time.Millisecond)
	done := make(chan struct{})
	go func() { f.a.agentConsole.closeAll(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown did not join preauth sockets")
	}
}
func TestV18PendingTicketExpiresAndCannotCrossOwnerSession(t *testing.T) {
	f := v18New(t)
	f.start(t)
	ticket := f.ticket(t)
	f.a.agentConsole.mu.Lock()
	p := f.a.agentConsole.tickets[secure.HashToken(ticket)]
	p.Expires = time.Now().Add(-time.Second)
	f.a.agentConsole.tickets[secure.HashToken(ticket)] = p
	f.a.agentConsole.mu.Unlock()
	s := f.open(t, ticket)
	if _, e := s.Read(); e == nil {
		t.Fatal("expired permit")
	}
	ticket = f.ticket(t)
	cookie, _ := consoleLogin(t, f.ts)
	w, e := f.browser(t, "/api/v1/agents/h/agent-console-stream", cookie, f.ts.URL)
	if e != nil {
		t.Fatal(e)
	}
	defer w.Close()
	s = &ac.Socket{WS: w}
	w.SetDeadline(time.Now().Add(2 * time.Second))
	s.Send(ac.Message{Type: "authenticate", Ticket: ticket, Cols: 80, Rows: 24})
	if _, e := s.Read(); e == nil {
		t.Fatal("other cookie consumed permit")
	}
	if f.opens.Load() != 0 {
		t.Fatal("bad permit opened shell")
	}
}

func TestV18RepeatedInputAfterSuccessfulExecutionIsNotReplayed(t *testing.T) {
	f := v18New(t)
	f.start(t)
	s := f.open(t, f.ticket(t))
	v18Ready(t, s)
	frame := ac.Message{Type: "input", Seq: 1, Data: "once-only"}
	if e := s.Send(frame); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Read(); e != nil {
		t.Fatal(e)
	}
	s.Send(frame)
	if _, e := s.Read(); e == nil {
		t.Fatal("replayed sequence accepted")
	}
	select {
	case <-f.inputs:
	case <-time.After(time.Second):
		t.Fatal("first input missing")
	}
	select {
	case <-f.inputs:
		t.Fatal("input repeated")
	default:
	}
}
func TestV18TicketNeedsRecentOwnerAndAgentHeaderCannotUseCookie(t *testing.T) {
	f := v18New(t)
	f.start(t)
	_, info := f.request(t, "GET", "/api/v1/agents/h/agent-console", nil, f.cookie, f.csrf)
	body := map[string]any{"target_revision": info["channel_revision"]}
	if _, e := f.a.Store.DB.Exec(`UPDATE admin_sessions SET recent_auth_until=?`, time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)); e != nil {
		t.Fatal(e)
	}
	status, _ := f.request(t, "POST", "/api/v1/agents/h/agent-console-ticket", body, f.cookie, f.csrf)
	if status != 401 {
		t.Fatal("old auth", status)
	}
	if _, e := f.a.Store.DB.Exec(`UPDATE admin_users SET role='viewer'`); e != nil {
		t.Fatal(e)
	}
	status, _ = f.request(t, "POST", "/api/v1/agents/h/agent-console-ticket", body, f.cookie, f.csrf)
	if status != 403 {
		t.Fatal("viewer", status)
	}
	r, _ := http.NewRequest("GET", f.ts.URL+"/api/v1/agent/console-channel", nil)
	r.Header.Set("Origin", f.ts.URL)
	r.Header.Set("Authorization", "Bearer "+f.binding.Credential)
	r.Header.Set("X-Monik-Agent-Id", "h")
	r.Header.Set("X-Monik-Worker-Session", f.binding.WorkerSession)
	r.AddCookie(&http.Cookie{Name: "monik_session", Value: f.cookie})
	res, e := f.ts.Client().Do(r)
	if e != nil {
		t.Fatal(e)
	}
	defer res.Body.Close()
	if res.StatusCode != 403 {
		t.Fatal("mixed authentication", res.StatusCode)
	}
}

func TestV18RevocationDuringAuditDoesNotOpenShell(t *testing.T) {
	f := v18New(t)
	f.start(t)
	ticket := f.ticket(t)
	// Deterministically order revocation after durable intent but before transport
	// dispatch, equivalent to an owner logout racing a delayed database write.
	if _, e := f.a.Store.DB.Exec(`CREATE TRIGGER revoke_at_intent AFTER INSERT ON audit_events WHEN NEW.action='agent_console.open' BEGIN DELETE FROM admin_sessions; END`); e != nil {
		t.Fatal(e)
	}
	s := f.open(t, ticket)
	if _, e := s.Read(); e == nil {
		t.Fatal("shell opened after session revoked during audit")
	}
	if f.opens.Load() != 0 {
		t.Fatal("local shell started after revoke")
	}
}
