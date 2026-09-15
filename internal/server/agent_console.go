package server

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	ac "github.com/alinescafs3mp-afk/monik_v2/internal/agentconsole"
	"github.com/alinescafs3mp-afk/monik_v2/internal/idgen"
	"github.com/alinescafs3mp-afk/monik_v2/internal/secure"
	"github.com/alinescafs3mp-afk/monik_v2/internal/storage"
	"golang.org/x/net/websocket"
)

type agentConsolePermit struct {
	Session, Agent, Channel string
	Expires                 time.Time
}
type agentConsoleRelay struct {
	id     string
	out    chan ac.Message
	cancel context.CancelFunc
}
type agentConsolePeer struct {
	channel, user, worker string
	socket                *ac.Socket
	cancel                context.CancelFunc
	ready                 bool
	relay                 *agentConsoleRelay
}
type agentConsoleState struct {
	mu       sync.Mutex
	closing  bool
	peers    map[string]*agentConsolePeer
	tickets  map[string]agentConsolePermit
	browsers map[string]context.CancelFunc
	wg       sync.WaitGroup
}

func (c *agentConsoleState) closeAll() {
	c.mu.Lock()
	c.closing = true
	c.tickets = nil
	var cancel []context.CancelFunc
	for _, p := range c.peers {
		cancel = append(cancel, p.cancel)
	}
	for _, f := range c.browsers {
		cancel = append(cancel, f)
	}
	c.mu.Unlock()
	for _, f := range cancel {
		f()
	}
	c.wg.Wait()
}
func plainUser(s string) bool {
	if len(s) < 1 || len(s) > 32 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return s != "root" && s != "monik"
}
func (a *App) validConsoleAgent(r *http.Request) bool {
	ag, _, ok := a.agentFromCurrent(r)
	if !ok || a.Cfg.RestoreMode || ag.Archived || ag.Conflict || ag.SessionID == "" || ag.SessionID != r.Header.Get("X-Monik-Worker-Session") || ag.LastLiveAt == nil {
		return false
	}
	age := a.Clock.Now().Sub(*ag.LastLiveAt)
	return age >= 0 && age <= 30*time.Second
}
func (a *App) handleAgentConsoleChannel(w http.ResponseWriter, r *http.Request) {
	if !consoleOrigin(r) || r.Header.Get("Cookie") != "" || r.URL.RawQuery != "" || len(r.Header.Get("Authorization")) != 71 || !a.rateLimit("console-agent-ip:"+clientIP(r), 60, time.Minute) {
		a.writeErr(w, 403, "console_denied", "authenticated HTTPS agent connection required")
		return
	}
	if !a.validConsoleAgent(r) {
		a.writeErr(w, 401, "console_denied", "current live enrolled agent required")
		return
	}
	id, _, _ := bearer(r)
	if !a.rateLimit("console-agent:"+id, 12, time.Minute) {
		a.writeErr(w, 429, "rate_limited", "console reconnect rate exceeded")
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	channel, e := idgen.Secret(32)
	if e != nil {
		a.writeErr(w, 500, "entropy", "console unavailable")
		return
	}
	p := &agentConsolePeer{channel: channel, worker: r.Header.Get("X-Monik-Worker-Session"), cancel: cancel}
	c := &a.agentConsole
	c.mu.Lock()
	if c.closing || c.peers[id] != nil || len(c.peers) >= 128 {
		c.mu.Unlock()
		a.writeErr(w, 409, "console_capacity", "console channel already active or capacity reached")
		return
	}
	if c.peers == nil {
		c.peers = map[string]*agentConsolePeer{}
	}
	c.peers[id] = p
	c.wg.Add(1)
	c.mu.Unlock()
	defer func() {
		cancel()
		c.mu.Lock()
		if c.peers[id] == p {
			delete(c.peers, id)
		}
		relay := p.relay
		p.relay = nil
		c.mu.Unlock()
		if relay != nil {
			relay.cancel()
		}
		c.wg.Done()
	}()
	server := websocket.Server{Handshake: func(_ *websocket.Config, req *http.Request) error {
		if !a.validConsoleAgent(req) {
			return fmt.Errorf("agent revoked")
		}
		return nil
	}, Handler: func(ws *websocket.Conn) {
		sock := &ac.Socket{WS: ws}
		defer sock.Close()
		c.mu.Lock()
		p.socket = sock
		c.mu.Unlock()
		joined := make(chan struct{})
		go func() { defer close(joined); <-ctx.Done(); sock.Close() }()
		defer func() { cancel(); <-joined }()
		if sock.Send(ac.Message{Type: "controller", Data: a.ControllerID()}) != nil {
			return
		}
		_ = ws.SetReadDeadline(time.Now().Add(8 * time.Second))
		hello, e := sock.Read()
		if e != nil || hello.Type != "hello" || !plainUser(hello.User) || hello.Data != "" || hello.Session != "" || hello.Ticket != "" || hello.Message != "" || hello.Seq != 0 || hello.Cols != 0 || hello.Rows != 0 {
			return
		}
		c.mu.Lock()
		p.user = hello.User
		p.ready = true
		c.mu.Unlock()
		a.Store.Audit("agent", "agent_console.channel", id, "outbound channel ready; no shell opened")
		pulseDone := make(chan struct{})
		go func() {
			defer close(pulseDone)
			t := time.NewTicker(time.Second)
			defer t.Stop()
			n := 0
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					if !a.validConsoleAgent(r) {
						cancel()
						return
					}
					n++
					if n%10 == 0 && sock.Send(ac.Message{Type: "ping"}) != nil {
						cancel()
						return
					}
				}
			}
		}()
		defer func() { cancel(); <-pulseDone }()
		window := time.Now()
		frames := 0
		for {
			_ = ws.SetReadDeadline(time.Now().Add(30 * time.Second))
			m, e := sock.Read()
			if e != nil {
				return
			}
			if time.Since(window) >= time.Second {
				window = time.Now()
				frames = 0
			}
			frames++
			if frames > 512 {
				return
			}
			if m.Type == "pong" {
				if m != (ac.Message{Type: "pong"}) {
					return
				}
				continue
			}
			if !ac.ID(m.Session) {
				return
			}
			c.mu.Lock()
			rel := p.relay
			c.mu.Unlock()
			// Late output from a closed terminal is never routed into a new terminal.
			if rel == nil || rel.id != m.Session {
				continue
			}
			switch m.Type {
			case "ready":
				if m.User != p.user || m.Message != "" || m.Data != "" || m.Ticket != "" || m.Seq != 0 || m.Rows != 0 || m.Cols != 0 {
					return
				}
			case "output":
				if _, e := ac.OutputBytes(m); e != nil {
					return
				}
			case "closed":
				if m.Data != "" || m.Ticket != "" || m.User != "" || m.Seq != 0 || m.Cols != 0 || m.Rows != 0 {
					return
				}
			default:
				return
			}
			select {
			case rel.out <- m:
			case <-ctx.Done():
				return
			default:
				rel.cancel()
				return
			}
		}
	}}
	server.ServeHTTP(w, r)
}
func (a *App) agentConsoleUsable(id string) bool {
	ag, e := a.Store.Agent(id)
	return e == nil && !ag.Revoked && !ag.Archived && !ag.Conflict && !a.Cfg.RestoreMode
}
func (a *App) handleAgentConsoleInfo(w http.ResponseWriter, r *http.Request, s *storage.Session) {
	if s.Role != "owner" {
		a.writeErr(w, 403, "forbidden", "owner required")
		return
	}
	id := r.PathValue("id")
	if !a.agentConsoleUsable(id) {
		a.writeErr(w, 409, "console_disabled", "machine unavailable or controller restoring")
		return
	}
	c := &a.agentConsole
	c.mu.Lock()
	p := c.peers[id]
	enabled := !c.closing && p != nil && p.ready
	user, channel := "", ""
	if enabled {
		user = p.user
		channel = p.channel
	}
	c.mu.Unlock()
	reason := "Агентский канал не подключён. Нужны новая версия агента и локально разрешённая консоль. Включение не открывает входящие сетевые порты."
	if enabled {
		reason = ""
	}
	a.writeJSON(w, 200, map[string]any{"enabled": enabled, "reason": reason, "transport": "agent", "username": user, "channel_revision": channel, "max_minutes": 60, "idle_minutes": 10})
}
func (a *App) handleAgentConsoleTicket(w http.ResponseWriter, r *http.Request, s *storage.Session) {
	if s.Role != "owner" {
		a.writeErr(w, 403, "forbidden", "owner required")
		return
	}
	if !a.requireRecent(w, s) {
		return
	}
	if r.TLS == nil || !a.agentConsoleUsable(r.PathValue("id")) {
		a.writeErr(w, 409, "console_disabled", "active HTTPS controller and machine required")
		return
	}
	if !a.rateLimit("agent-console-start:"+s.UserID, 12, time.Minute) {
		a.writeErr(w, 429, "rate_limited", "too many console starts")
		return
	}
	var req struct {
		TargetRevision string `json:"target_revision"`
	}
	if parseJSONStrictLimit(r, &req, 1024) != nil || !ac.ID(req.TargetRevision) {
		a.writeErr(w, 400, "invalid_revision", "displayed console channel required")
		return
	}
	id := r.PathValue("id")
	c := &a.agentConsole
	c.mu.Lock()
	defer c.mu.Unlock()
	p := c.peers[id]
	if c.closing || p == nil || !p.ready || p.channel != req.TargetRevision {
		a.writeErr(w, 409, "console_changed", "console channel changed; reload before connecting")
		return
	}
	if p.relay != nil {
		a.writeErr(w, 409, "console_busy", "machine already has a terminal")
		return
	}
	now := a.Clock.Now()
	if c.tickets == nil {
		c.tickets = map[string]agentConsolePermit{}
	}
	for k, t := range c.tickets {
		if !now.Before(t.Expires) {
			delete(c.tickets, k)
		}
	}
	if len(c.tickets) >= 32 {
		a.writeErr(w, 429, "console_capacity", "ticket limit")
		return
	}
	token, e := idgen.Secret(32)
	if e != nil {
		a.writeErr(w, 500, "entropy", "ticket unavailable")
		return
	}
	c.tickets[secure.HashToken(token)] = agentConsolePermit{Session: s.ID, Agent: id, Channel: p.channel, Expires: now.Add(30 * time.Second)}
	a.writeJSON(w, 200, map[string]any{"ticket": token, "expires_in_seconds": 30})
}
func (a *App) handleAgentConsoleSocket(w http.ResponseWriter, r *http.Request, s *storage.Session) {
	if s.Role != "owner" || !recentOK(s, a.Clock.Now()) || !consoleOrigin(r) || r.URL.RawQuery != "" || !a.agentConsoleUsable(r.PathValue("id")) {
		a.writeErr(w, 403, "console_denied", "owner, recent authentication and same HTTPS origin required")
		return
	}
	if !a.rateLimit("agent-console-ws:"+s.UserID, 24, time.Minute) {
		a.writeErr(w, 429, "rate_limited", "socket rate exceeded")
		return
	}
	server := websocket.Server{Handshake: func(_ *websocket.Config, req *http.Request) error {
		if !consoleOrigin(req) {
			return fmt.Errorf("origin denied")
		}
		return nil
	}, Handler: func(ws *websocket.Conn) { a.agentConsoleBrowser(ws, r, s) }}
	server.ServeHTTP(w, r)
}
func (a *App) agentConsoleBrowser(ws *websocket.Conn, r *http.Request, s *storage.Session) {
	sock := &ac.Socket{WS: ws}
	defer sock.Close()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	c := &a.agentConsole
	connectionID := idgen.New()
	c.mu.Lock()
	if c.closing || len(c.browsers) >= 8 {
		c.mu.Unlock()
		return
	}
	if c.browsers == nil {
		c.browsers = map[string]context.CancelFunc{}
	}
	c.browsers[connectionID] = cancel
	c.wg.Add(1)
	c.mu.Unlock()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); <-ctx.Done(); sock.Close() }()
	defer func() {
		cancel()
		sock.Close()
		wg.Wait()
		c.mu.Lock()
		delete(c.browsers, connectionID)
		c.mu.Unlock()
		c.wg.Done()
	}()
	_ = ws.SetReadDeadline(time.Now().Add(8 * time.Second))
	hello, e := sock.Read()
	if e != nil || hello.Type != "authenticate" || !ac.ID(hello.Ticket) || !ac.Size(hello.Cols, hello.Rows) || hello.Session != "" || hello.Data != "" || hello.User != "" || hello.Message != "" || hello.Seq != 0 {
		return
	}
	if !a.consoleOwnerCurrent(s) {
		return
	}
	id := r.PathValue("id")
	sessionID, e := idgen.Secret(32)
	if e != nil {
		return
	}
	rel := &agentConsoleRelay{id: sessionID, out: make(chan ac.Message, 32), cancel: cancel}
	c.mu.Lock()
	p := c.peers[id]
	key := secure.HashToken(hello.Ticket)
	permit, ok := c.tickets[key]
	active := 0
	for _, peer := range c.peers {
		if peer.relay != nil {
			active++
		}
	}
	if c.closing || !ok || permit.Session != s.ID || permit.Agent != id || !a.Clock.Now().Before(permit.Expires) || p == nil || !p.ready || p.channel != permit.Channel || p.relay != nil || active >= 4 {
		c.mu.Unlock()
		return
	}
	delete(c.tickets, key)
	p.relay = rel
	c.mu.Unlock()
	// Close the agent channel on termination. This is deliberately conservative:
	// it kills the local socket immediately and provides no stale-frame resume.
	defer func() {
		cancel()
		p.cancel()
		c.mu.Lock()
		if p.relay == rel {
			p.relay = nil
		}
		c.mu.Unlock()
	}()
	if _, e = a.Store.DB.ExecContext(ctx, `INSERT INTO audit_events(at,actor,action,entity,detail) VALUES(?,?,?,?,?)`, a.Clock.Now().UTC().Format(time.RFC3339Nano), s.Username, "agent_console.open", id, "ephemeral terminal as "+p.user+"; no input/output recording"); e != nil {
		return
	}
	defer a.Store.Audit(s.Username, "agent_console.close", id, "terminal closed; no input replay")
	// A delayed audit commit must not outlive the authorization it recorded.
	if !a.consoleOwnerCurrent(s) || !a.agentConsoleUsable(id) || ctx.Err() != nil {
		return
	}
	if p.socket.Send(ac.Message{Type: "open", Session: sessionID, Cols: hello.Cols, Rows: hello.Rows}) != nil {
		return
	}
	inputActivity := make(chan struct{}, 1)
	var inputReady atomic.Bool
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer cancel()
		last := int64(0)
		window := time.Now()
		frames := 0
		for {
			_ = ws.SetReadDeadline(time.Now().Add(35 * time.Second))
			m, e := sock.Read()
			if e != nil {
				return
			}
			if time.Since(window) >= time.Second {
				frames = 0
				window = time.Now()
			}
			frames++
			if frames > 128 {
				return
			}
			if m.Type == "pong" {
				if m != (ac.Message{Type: "pong"}) {
					return
				}
				continue
			}
			if !inputReady.Load() || m.Session != "" || !ac.ValidateInput(m, last) || !a.consoleOwnerCurrent(s) || !a.agentConsoleUsable(id) {
				return
			}
			last = m.Seq
			if m.Type == "close" {
				return
			}
			m.Session = sessionID
			if p.socket.Send(m) != nil {
				return
			}
			if m.Type == "input" {
				select {
				case inputActivity <- struct{}{}:
				default:
				}
			}
		}
	}()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	started, lastInput := time.Now(), time.Now()
	total := 0
	ready := false
	n := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-inputActivity:
			lastInput = time.Now()
		case <-ticker.C:
			n++
			if !a.consoleOwnerCurrent(s) || !a.agentConsoleUsable(id) || time.Since(started) > ac.Lifetime || time.Since(lastInput) > ac.Idle {
				return
			}
			if !ready && time.Since(started) > 10*time.Second {
				_ = sock.Send(ac.Message{Type: "error", Message: "Агент не подтвердил запуск терминала."})
				return
			}
			if n%10 == 0 && sock.Send(ac.Message{Type: "ping"}) != nil {
				return
			}
		case m := <-rel.out:
			switch m.Type {
			case "ready":
				if ready {
					return
				}
				ready = true
				inputReady.Store(true)
				m.Message = "Подключено через агент. Пользователь ОС: " + p.user
			case "output":
				if !ready {
					return
				}
				b, e := ac.OutputBytes(m)
				if e != nil {
					return
				}
				total += len(b)
				if total > ac.OutputBudget {
					return
				}
			case "closed":
				_ = sock.Send(ac.Message{Type: "closed", Message: "Сеанс агента завершён. Автоповтора нет."})
				return
			default:
				return
			}
			m.Session = ""
			if sock.Send(m) != nil {
				return
			}
		}
	}
}

// No network terminal input is interpolated in controller commands: this file
// contains routing/authentication only. Metadata logs omit tickets and payloads.
