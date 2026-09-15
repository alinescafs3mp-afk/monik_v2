package server

// The console is an owner-operated SSH gateway. It neither executes on the
// controller nor grants the monitoring worker additional OS privileges.
import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/idgen"
	"github.com/alinescafs3mp-afk/monik_v2/internal/storage"
	"golang.org/x/crypto/ssh"
	"golang.org/x/net/websocket"
)

type consoleTarget struct {
	Host        string `json:"host"`
	Port        int    `json:"port"`
	Username    string `json:"username"`
	Fingerprint string `json:"host_key_sha256"`
	HostKeyType string `json:"host_key_type,omitempty"`
}
type consoleTicket struct {
	Session, Agent, Target string
	Expires                time.Time
}
type consoleState struct {
	mu          sync.Mutex
	tickets     map[string]consoleTicket
	active      map[string]func()
	connections map[string]func()
	closing     bool
	// sessions tracks in-flight consoleSession goroutines so Close can
	// finish their final audit writes before the store is closed.
	sessions sync.WaitGroup
}

func (c *consoleState) closeAll() {
	c.mu.Lock()
	c.closing = true
	c.tickets = nil
	fns := make([]func(), 0, len(c.active)+len(c.connections))
	for _, f := range c.connections {
		fns = append(fns, f)
	}
	for _, f := range c.active {
		fns = append(fns, f)
	}
	c.mu.Unlock()
	for _, f := range fns {
		f()
	}
	c.sessions.Wait()
}
func (a *App) consoleTarget(id string) (consoleTarget, error) {
	var out consoleTarget
	if a.Cfg.RestoreMode {
		return out, fmt.Errorf("console disabled while restored controller state awaits reconciliation")
	}
	agent, err := a.Store.Agent(id)
	if err != nil {
		return out, fmt.Errorf("machine not found")
	}
	if agent.Revoked || agent.Archived {
		return out, fmt.Errorf("machine access is revoked or archived")
	}
	cfg, _, err := a.readConsoleConfiguration()
	if err != nil {
		return out, err
	}
	out, ok := cfg.Targets[id]
	if !ok {
		return out, fmt.Errorf("console not enabled for this machine")
	}
	return out, validateConsoleTarget(out)
}

func validateConsoleTarget(t consoleTarget) error {
	if _, ok := consoleHostKeyAlgorithms(t.HostKeyType); !ok {
		return fmt.Errorf("unsupported SSH host key type")
	}
	ip, err := netip.ParseAddr(t.Host)
	if err != nil || ip.Zone() != "" || ip.Unmap().IsUnspecified() || ip.Unmap().IsMulticast() || ip.Unmap().IsLinkLocalUnicast() || ip.Unmap().IsLinkLocalMulticast() {
		return fmt.Errorf("console host must be an exact permitted unicast IP, not a hostname or metadata destination")
	}
	ip = ip.Unmap()
	if ip.String() == "100.100.100.200" {
		return fmt.Errorf("metadata destination is forbidden")
	}
	if t.Port < 1 || t.Port > 65535 || len(t.Username) == 0 || len(t.Username) > 128 {
		return fmt.Errorf("invalid SSH account or port")
	}
	for _, r := range t.Username {
		if r < 33 || r == 127 {
			return fmt.Errorf("invalid SSH account")
		}
	}
	b, e := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(t.Fingerprint, "SHA256:"))
	if !strings.HasPrefix(t.Fingerprint, "SHA256:") || e != nil || len(b) != 32 {
		return fmt.Errorf("independently verified SHA256 SSH host-key fingerprint required")
	}
	return nil
}

// A SHA256 fingerprint does not reveal the key type. Pin negotiation to the
// independently checked key instead of accidentally choosing another host key.
func consoleHostKeyAlgorithms(kind string) ([]string, bool) {
	switch kind {
	case "":
		return nil, true // backwards-compatible protected files
	case ssh.KeyAlgoED25519, ssh.KeyAlgoECDSA256, ssh.KeyAlgoECDSA384, ssh.KeyAlgoECDSA521:
		return []string{kind}, true
	case ssh.KeyAlgoRSA:
		return []string{ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256}, true
	default:
		return nil, false
	}
}

func targetIdentity(t consoleTarget) string { b, _ := json.Marshal(t); return string(b) }
func (a *App) handleConsoleInfo(w http.ResponseWriter, r *http.Request, s *storage.Session) {
	if s.Role != "owner" {
		a.writeErr(w, 403, "forbidden", "owner role required")
		return
	}
	id := r.PathValue("id")
	agent, e := a.Store.Agent(id)
	if e != nil {
		a.writeErr(w, 404, "not_found", "machine not found")
		return
	}
	if a.Cfg.RestoreMode || agent.Revoked || agent.Archived {
		a.writeJSON(w, 200, map[string]any{"enabled": false, "configurable": false, "reason": "Доступ к машине отозван, архивирован или контроллер ожидает восстановления."})
		return
	}
	cfg, revision, e := a.readConsoleConfiguration()
	if e != nil {
		a.writeJSON(w, 200, map[string]any{"enabled": false, "configurable": false, "reason": e.Error()})
		return
	}
	t, enabled := cfg.Targets[id]
	out := map[string]any{"enabled": enabled, "configurable": true, "config_revision": revision, "max_minutes": 60, "idle_minutes": 10, "transport": "ssh"}
	if enabled {
		out["target"] = t
		out["target_revision"] = consoleConfigRevision([]byte(targetIdentity(t)))
	} else {
		out["reason"] = "SSH-консоль пока не настроена для этой машины. Укажите параметры подключения ниже."
	}
	a.writeJSON(w, 200, out)
}

func (a *App) handleConsoleTicket(w http.ResponseWriter, r *http.Request, s *storage.Session) {
	if s.Role != "owner" {
		a.writeErr(w, 403, "forbidden", "owner role required")
		return
	}
	if !a.requireRecent(w, s) {
		return
	}
	if r.TLS == nil {
		a.writeErr(w, 400, "tls_required", "console requires direct HTTPS or TLS termination at Monik")
		return
	}
	if !a.rateLimit("console:"+s.UserID, 12, time.Minute) {
		a.writeErr(w, 429, "rate_limited", "too many console starts")
		return
	}
	id := r.PathValue("id")
	target, err := a.consoleTarget(id)
	if err != nil {
		a.writeErr(w, 409, "console_disabled", err.Error())
		return
	}
	// Bind credentials to what the owner actually saw, not merely the current
	// server mapping. Older tabs must reload instead of connecting elsewhere.
	body, err := io.ReadAll(io.LimitReader(r.Body, 1025))
	var request struct {
		TargetRevision string `json:"target_revision"`
	}
	if err != nil || len(body) > 1024 || exactConsoleFields(body, "target_revision") != nil {
		a.writeErr(w, 400, "invalid_request", "bounded ticket request required")
		return
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&request) != nil || request.TargetRevision != consoleConfigRevision([]byte(targetIdentity(target))) {
		a.writeErr(w, 409, "console_target_changed", "SSH-настройки изменились или вкладка устарела. Перечитайте настройки перед вводом SSH-пароля.")
		return
	}
	token, err := idgen.Secret(32)
	if err != nil {
		a.writeErr(w, 500, "entropy", "ticket unavailable")
		return
	}
	now := a.Clock.Now()
	a.console.mu.Lock()
	defer a.console.mu.Unlock()
	if a.console.closing {
		a.writeErr(w, 503, "shutting_down", "controller is stopping")
		return
	}
	if a.console.tickets == nil {
		a.console.tickets = map[string]consoleTicket{}
	}
	for k, v := range a.console.tickets {
		if !now.Before(v.Expires) {
			delete(a.console.tickets, k)
		}
	}
	if len(a.console.tickets) >= 32 || len(a.console.active) >= 4 {
		a.writeErr(w, 429, "console_capacity", "console capacity reached")
		return
	}
	a.console.tickets[token] = consoleTicket{Session: s.ID, Agent: id, Target: targetIdentity(target), Expires: now.Add(30 * time.Second)}
	a.writeJSON(w, 200, map[string]any{"ticket": token, "expires_in_seconds": 30})
}
func (a *App) takeConsoleTicket(token string, s *storage.Session, id string, target consoleTarget) bool {
	a.console.mu.Lock()
	defer a.console.mu.Unlock()
	if a.console.closing {
		return false
	}
	t, ok := a.console.tickets[token]
	delete(a.console.tickets, token)
	return ok && t.Session == s.ID && t.Agent == id && t.Target == targetIdentity(target) && a.Clock.Now().Before(t.Expires)
}

type consoleMessage struct {
	Type       string `json:"type"`
	Ticket     string `json:"ticket,omitempty"`
	Password   string `json:"password,omitempty"`
	PrivateKey string `json:"private_key,omitempty"`
	Passphrase string `json:"passphrase,omitempty"`
	Data       string `json:"data,omitempty"`
	Cols       int    `json:"cols,omitempty"`
	Rows       int    `json:"rows,omitempty"`
}

func consoleSize(cols, rows int) bool { return cols >= 20 && cols <= 300 && rows >= 5 && rows <= 120 }
func consoleOrigin(r *http.Request) bool {
	return r.TLS != nil && r.Header.Get("Origin") == "https://"+r.Host
}
func (a *App) handleConsoleSocket(w http.ResponseWriter, r *http.Request, s *storage.Session) {
	if s.Role != "owner" || !recentOK(s, a.Clock.Now()) || !consoleOrigin(r) || r.URL.RawQuery != "" {
		a.writeErr(w, 403, "console_denied", "owner, recent authentication and same HTTPS origin required")
		return
	}
	id := r.PathValue("id")
	target, err := a.consoleTarget(id)
	if err != nil {
		a.writeErr(w, 409, "console_disabled", err.Error())
		return
	}
	wsServer := websocket.Server{
		Handshake: func(_ *websocket.Config, req *http.Request) error {
			if !consoleOrigin(req) {
				return fmt.Errorf("origin denied")
			}
			return nil
		},
		Handler: func(ws *websocket.Conn) { a.consoleSession(ws, r, s, id, target) },
	}
	wsServer.ServeHTTP(w, r)
}
func (a *App) consoleSession(ws *websocket.Conn, r *http.Request, s *storage.Session, id string, target consoleTarget) {
	defer ws.Close()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	closeConnection := func() { cancel(); _ = ws.Close() }
	// Include pre-authentication sockets in shutdown; hijacked HTTP connections
	// are not drained by http.Server.Shutdown itself.
	connectionID := idgen.New()
	a.console.mu.Lock()
	if a.console.closing || len(a.console.connections) >= 8 {
		a.console.mu.Unlock()
		return
	}
	if a.console.connections == nil {
		a.console.connections = map[string]func(){}
	}
	a.console.connections[connectionID] = closeConnection
	a.console.sessions.Add(1)
	a.console.mu.Unlock()
	defer func() {
		a.console.mu.Lock()
		delete(a.console.connections, connectionID)
		a.console.mu.Unlock()
		a.console.sessions.Done()
	}()
	ws.MaxPayloadBytes = 64 << 10
	_ = ws.SetReadDeadline(time.Now().Add(10 * time.Second))
	var hello consoleMessage
	if err := readSSHMessage(ws, &hello); err != nil || hello.Type != "authenticate" || !a.takeConsoleTicket(hello.Ticket, s, id, target) {
		return
	}
	if hello.Data != "" || !consoleSize(hello.Cols, hello.Rows) || len(hello.Password) > 1024 || len(hello.PrivateKey) > 32<<10 || len(hello.Passphrase) > 1024 {
		return
	}
	if (hello.Password == "") == (hello.PrivateKey == "") {
		return
	}
	if !a.consoleOwnerCurrent(s) {
		return
	}
	a.console.mu.Lock()
	if a.console.active == nil {
		a.console.active = map[string]func(){}
	}
	if _, exists := a.console.active[id]; a.console.closing || exists || len(a.console.active) >= 4 {
		a.console.mu.Unlock()
		_ = websocket.JSON.Send(ws, map[string]string{"type": "error", "message": "Для машины уже открыта консоль либо достигнут лимит."})
		return
	}
	a.console.active[id] = closeConnection
	a.console.mu.Unlock()
	defer func() {
		a.console.mu.Lock()
		delete(a.console.active, id)
		a.console.mu.Unlock()
	}()
	var writeMu sync.Mutex
	send := func(v any) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		_ = ws.SetWriteDeadline(time.Now().Add(5 * time.Second))
		return websocket.JSON.Send(ws, v)
	}
	fail := func(msg string) {
		a.Store.Audit(s.Username, "console.error", id, msg)
		_ = send(map[string]string{"type": "error", "message": msg})
	}
	auth := []ssh.AuthMethod{}
	if hello.Password != "" {
		auth = append(auth, ssh.Password(hello.Password))
	} else {
		var key ssh.Signer
		var e error
		if hello.Passphrase != "" {
			key, e = ssh.ParsePrivateKeyWithPassphrase([]byte(hello.PrivateKey), []byte(hello.Passphrase))
		} else {
			key, e = ssh.ParsePrivateKey([]byte(hello.PrivateKey))
		}
		if e != nil {
			fail("Не удалось прочитать приватный ключ. Проверьте формат и пароль ключа.")
			return
		}
		auth = append(auth, ssh.PublicKeys(key))
	}
	hello = consoleMessage{Cols: hello.Cols, Rows: hello.Rows}
	if _, err := a.Store.DB.ExecContext(ctx, `INSERT INTO audit_events(at,actor,action,entity,detail) VALUES(?,?,?,?,?)`, a.Clock.Now().UTC().Format(time.RFC3339Nano), s.Username, "console.connect.requested", id, "fixed SSH destination; credentials not recorded"); err != nil {
		fail("Журнал недоступен. SSH-подключение не начато.")
		return
	}
	addr := net.JoinHostPort(target.Host, fmt.Sprint(target.Port))
	conn, err := (&net.Dialer{Timeout: 8 * time.Second}).DialContext(ctx, "tcp", addr)
	if err != nil {
		fail("SSH недоступен с контроллера: проверьте адрес, порт и маршрут.")
		return
	}
	defer conn.Close()
	go func() { <-ctx.Done(); _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	mismatch := false
	algorithms, _ := consoleHostKeyAlgorithms(target.HostKeyType)
	cfg := &ssh.ClientConfig{User: target.Username, Auth: auth, HostKeyAlgorithms: algorithms, HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
		current, e := a.consoleTarget(id)
		if e != nil || targetIdentity(current) != targetIdentity(target) || !a.consoleOwnerCurrent(s) {
			return fmt.Errorf("console authorization changed")
		}
		if subtle.ConstantTimeCompare([]byte(ssh.FingerprintSHA256(key)), []byte(target.Fingerprint)) != 1 {
			mismatch = true
			return fmt.Errorf("host key mismatch")
		}
		return nil
	}}
	clientConn, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	// ClientConfig stays immutable during the SSH connection.
	auth = nil
	if err != nil {
		if mismatch {
			fail("Ключ SSH-сервера изменился. Соединение запрещено до независимой сверки отпечатка.")
		} else {
			fail("SSH-аутентификация или согласование протокола не прошли.")
		}
		return
	}
	_ = conn.SetDeadline(time.Time{})
	client := ssh.NewClient(clientConn, chans, reqs)
	defer client.Close()
	if !a.consoleOwnerCurrent(s) || ctx.Err() != nil {
		return
	}
	// PTY and shell requests must not hang forever before the session watchdog starts.
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	shell, err := client.NewSession()
	if err != nil {
		fail("SSH-сервер не открыл сеанс.")
		return
	}
	defer shell.Close()
	stdin, err := shell.StdinPipe()
	if err != nil {
		fail("SSH-ввод недоступен.")
		return
	}
	writer := &consoleOutput{send: send, limit: 16 << 20, abort: cancel}
	shell.Stdout = writer
	shell.Stderr = writer
	if err = shell.RequestPty("xterm-256color", hello.Rows, hello.Cols, ssh.TerminalModes{ssh.ECHO: 1}); err != nil {
		fail("SSH-сервер отказал в интерактивном терминале.")
		return
	}
	if !a.consoleOwnerCurrent(s) || ctx.Err() != nil {
		return
	}
	if err = shell.Shell(); err != nil {
		fail("SSH-сервер отказал в запуске интерактивной оболочки.")
		return
	}
	_ = conn.SetDeadline(time.Time{})
	_ = ws.SetReadDeadline(time.Time{})
	a.Store.Audit(s.Username, "console.open", id, "interactive SSH session; input/output not recorded")
	defer a.Store.Audit(s.Username, "console.close", id, "interactive SSH session closed")
	if send(map[string]string{"type": "ready", "message": "SSH подключён. Команды выполняются на выбранной машине."}) != nil {
		return
	}
	activity := make(chan struct{}, 1)
	done := make(chan error, 2)
	go func() { done <- shell.Wait() }()
	go func() {
		for {
			var msg consoleMessage
			if e := readSSHMessage(ws, &msg); e != nil {
				done <- e
				return
			}
			if msg.Ticket != "" || msg.Password != "" || msg.PrivateKey != "" || msg.Passphrase != "" {
				done <- fmt.Errorf("unexpected authentication data")
				return
			}
			if !a.consoleOwnerCurrent(s) {
				done <- fmt.Errorf("owner access revoked")
				return
			}
			switch msg.Type {
			case "input":
				if len(msg.Data) == 0 || len(msg.Data) > 4096 || msg.Cols != 0 || msg.Rows != 0 {
					done <- fmt.Errorf("input limit")
					return
				}
				if _, e := io.WriteString(stdin, msg.Data); e != nil {
					done <- e
					return
				}
			case "resize":
				if msg.Data != "" || !consoleSize(msg.Cols, msg.Rows) {
					done <- fmt.Errorf("size limit")
					return
				}
				if e := shell.WindowChange(msg.Rows, msg.Cols); e != nil {
					done <- e
					return
				}
			case "close":
				done <- nil
				return
			default:
				done <- fmt.Errorf("invalid terminal message")
				return
			}
			select {
			case activity <- struct{}{}:
			default:
			}
		}
	}()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	started, lastInput := time.Now(), time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			_ = send(map[string]string{"type": "closed", "message": "Сеанс завершён. Повторное подключение выполняется вручную."})
			return
		case <-activity:
			lastInput = time.Now()
		case <-ticker.C:
			if time.Since(started) > time.Hour || time.Since(lastInput) > 10*time.Minute {
				fail("Истекло время сеанса или ожидания ввода.")
				return
			}
			if !a.consoleOwnerCurrent(s) {
				fail("Вход отозван или истёк. Консоль закрыта.")
				return
			}
			current, e := a.consoleTarget(id)
			if e != nil || targetIdentity(current) != targetIdentity(target) {
				fail("Доступ или SSH-настройки изменены. Консоль закрыта.")
				return
			}
		}
	}
}

type consoleOutput struct {
	send  func(any) error
	total atomic.Int64
	limit int64
	abort func()
}

func (w *consoleOutput) Write(p []byte) (int, error) {
	if w.limit > 0 && w.total.Add(int64(len(p))) > w.limit {
		_ = w.send(map[string]string{"type": "error", "message": "Достигнут лимит вывода 16 МиБ. Закройте сеанс и подключитесь заново; используйте ограничение вывода команд."})
		if w.abort != nil {
			w.abort()
		}
		return 0, fmt.Errorf("terminal output budget exceeded")
	}
	total := 0
	for len(p) > 0 {
		n := len(p)
		if n > 8192 {
			n = 8192
		}
		if e := w.send(map[string]string{"type": "output", "data": base64.StdEncoding.EncodeToString(p[:n])}); e != nil {
			if w.abort != nil {
				w.abort()
			}
			return total, e
		}
		p = p[n:]
		total += n
	}
	return total, nil
}

// Security boundary shared by the retained SSH path: no duplicate/case-folded
// fields, no ambiguous JSON, and a strict bound before credential parsing.
func readSSHMessage(ws *websocket.Conn, dst *consoleMessage) error {
	var b []byte
	if e := websocket.Message.Receive(ws, &b); e != nil {
		return e
	}
	if len(b) > 64<<10 || exactConsoleFields(b, "type", "ticket", "password", "private_key", "passphrase", "data", "cols", "rows") != nil {
		return fmt.Errorf("invalid SSH console frame")
	}
	return json.Unmarshal(b, dst)
}

// Recheck role as well as cookie lifetime. A long-lived terminal must not keep
// owner authority after local account administration removes that role.
func (a *App) consoleOwnerCurrent(s *storage.Session) bool {
	if s == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var expires, role, userID string
	e := a.Store.DB.QueryRowContext(ctx, `SELECT s.expires_at,u.role,s.user_id FROM admin_sessions s JOIN admin_users u ON u.id=s.user_id WHERE s.id=?`, s.ID).Scan(&expires, &role, &userID)
	if e != nil || role != "owner" || userID != s.UserID {
		return false
	}
	// RFC3339Nano has variable precision and offsets: text order is not time order.
	deadline, e := time.Parse(time.RFC3339Nano, expires)
	return e == nil && a.Clock.Now().Before(deadline)
}
