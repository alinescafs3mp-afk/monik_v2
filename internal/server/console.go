package server

// The console is an owner-operated SSH gateway. It neither executes on the
// controller nor grants the monitoring worker additional OS privileges.
import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
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
}
type consoleTicket struct {
	Session, Agent, Target string
	Expires                time.Time
}
type consoleState struct {
	mu      sync.Mutex
	tickets map[string]consoleTicket
	active  map[string]func()
	// sessions tracks in-flight consoleSession goroutines so Close can
	// finish their final audit writes before the store is closed.
	sessions sync.WaitGroup
}

func (c *consoleState) closeAll() {
	c.mu.Lock()
	fns := make([]func(), 0, len(c.active))
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
	path := filepath.Join(a.Cfg.DataDir, "console-targets.json")
	f, err := os.Open(path)
	if err != nil {
		return out, fmt.Errorf("console disabled: configure protected console-targets.json on the controller")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 64<<10 {
		return out, fmt.Errorf("invalid console configuration file")
	}
	if info.Mode().Perm()&0022 != 0 {
		return out, fmt.Errorf("console configuration must not be group/world writable")
	}
	var cfg struct {
		Targets map[string]consoleTarget `json:"targets"`
	}
	d := json.NewDecoder(io.LimitReader(f, (64<<10)+1))
	d.DisallowUnknownFields()
	if err = d.Decode(&cfg); err != nil {
		return out, fmt.Errorf("invalid console configuration")
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return out, fmt.Errorf("trailing console configuration")
	}
	out, ok := cfg.Targets[id]
	if !ok {
		return out, fmt.Errorf("console not enabled for this machine")
	}
	return out, validateConsoleTarget(out)
}
func validateConsoleTarget(t consoleTarget) error {
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
func targetIdentity(t consoleTarget) string { b, _ := json.Marshal(t); return string(b) }
func (a *App) handleConsoleInfo(w http.ResponseWriter, r *http.Request, s *storage.Session) {
	if s.Role != "owner" {
		a.writeErr(w, 403, "forbidden", "owner role required")
		return
	}
	t, e := a.consoleTarget(r.PathValue("id"))
	if e != nil {
		a.writeJSON(w, 200, map[string]any{"enabled": false, "reason": e.Error()})
		return
	}
	a.writeJSON(w, 200, map[string]any{"enabled": true, "target": t, "max_minutes": 60, "idle_minutes": 10, "transport": "ssh"})
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
	token, err := idgen.Secret(32)
	if err != nil {
		a.writeErr(w, 500, "entropy", "ticket unavailable")
		return
	}
	now := a.Clock.Now()
	a.console.mu.Lock()
	defer a.console.mu.Unlock()
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
	if s.Role != "owner" || !recentOK(s, a.Clock.Now()) || !consoleOrigin(r) {
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
	ws.MaxPayloadBytes = 64 << 10
	_ = ws.SetReadDeadline(time.Now().Add(10 * time.Second))
	var hello consoleMessage
	if err := websocket.JSON.Receive(ws, &hello); err != nil || hello.Type != "authenticate" || !a.takeConsoleTicket(hello.Ticket, s, id, target) {
		return
	}
	if !consoleSize(hello.Cols, hello.Rows) || len(hello.Password) > 1024 || len(hello.PrivateKey) > 32<<10 || len(hello.Passphrase) > 1024 {
		return
	}
	if (hello.Password == "") == (hello.PrivateKey == "") {
		return
	}
	if a.Store.SessionStillValid(s.ID) != nil {
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	closeConnection := func() { cancel(); _ = ws.Close() }
	a.console.mu.Lock()
	if a.console.active == nil {
		a.console.active = map[string]func(){}
	}
	if _, exists := a.console.active[id]; exists || len(a.console.active) >= 4 {
		a.console.mu.Unlock()
		_ = websocket.JSON.Send(ws, map[string]string{"type": "error", "message": "Для машины уже открыта консоль либо достигнут лимит."})
		return
	}
	a.console.active[id] = closeConnection
	a.console.sessions.Add(1)
	a.console.mu.Unlock()
	defer func() {
		a.console.mu.Lock()
		delete(a.console.active, id)
		a.console.mu.Unlock()
		a.console.sessions.Done()
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
	cfg := &ssh.ClientConfig{User: target.Username, Auth: auth, HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
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
			if e := websocket.JSON.Receive(ws, &msg); e != nil {
				done <- e
				return
			}
			switch msg.Type {
			case "input":
				if len(msg.Data) > 16<<10 {
					done <- fmt.Errorf("input limit")
					return
				}
				if _, e := io.WriteString(stdin, msg.Data); e != nil {
					done <- e
					return
				}
			case "resize":
				if !consoleSize(msg.Cols, msg.Rows) {
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
			if a.Store.SessionStillValid(s.ID) != nil {
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
