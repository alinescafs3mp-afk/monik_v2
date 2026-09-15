package agentconsole

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/websocket"
)

type Binding struct {
	URL, AgentID, Credential, WorkerSession, ControllerID, TrustIdentity string
	TLS                                                                  *tls.Config
}

// Dial uses the agent's enrolled CA and exact HTTPS origin. Proxy environment,
// redirects and arbitrary tunnel destinations are deliberately not supported.
func Dial(ctx context.Context, b Binding) (*Socket, error) {
	u, e := url.Parse(b.URL)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.Trim(u.Path, "/") != "" || b.TLS == nil || b.TLS.InsecureSkipVerify || b.TLS.RootCAs == nil {
		return nil, fmt.Errorf("invalid enrolled console binding")
	}
	port := u.Port()
	if port == "" {
		port = "443"
	}
	dctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	c, e := (&net.Dialer{}).DialContext(dctx, "tcp", net.JoinHostPort(u.Hostname(), port))
	if e != nil {
		return nil, e
	}
	conf := b.TLS.Clone()
	if conf.MinVersion < tls.VersionTLS12 {
		conf.MinVersion = tls.VersionTLS12
	}
	conf.ServerName = u.Hostname()
	conf.NextProtos = []string{"http/1.1"}
	tc := tls.Client(c, conf)
	_ = tc.SetDeadline(time.Now().Add(8 * time.Second))
	if e = tc.HandshakeContext(dctx); e != nil {
		tc.Close()
		return nil, e
	}
	config, e := websocket.NewConfig("wss://"+u.Host+"/api/v1/agent/console-channel", "https://"+u.Host)
	if e != nil {
		tc.Close()
		return nil, e
	}
	config.Header = http.Header{"Authorization": []string{"Bearer " + b.Credential}, "X-Monik-Agent-Id": []string{b.AgentID}, "X-Monik-Worker-Session": []string{b.WorkerSession}}
	ws, e := websocket.NewClient(config, tc)
	if e != nil {
		tc.Close()
		return nil, e
	}
	_ = ws.SetDeadline(time.Time{})
	return &Socket{WS: ws}, nil
}

// RunLink permits a single terminal per live connection. Open instructions are
// never put into the durable job queue. Reconnection creates no shell.
func RunLink(ctx context.Context, s *Socket, controllerID, username string, dialLocal func(context.Context) (net.Conn, error)) error {
	defer s.Close()
	runctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	var mu sync.Mutex
	var active *Local
	activeID := ""
	lastSeq := int64(0)
	opened := false
	stop := func() {
		mu.Lock()
		l := active
		active = nil
		activeID = ""
		mu.Unlock()
		if l != nil {
			l.Close()
		}
	}
	defer func() { cancel(); s.Close(); stop(); wg.Wait() }()
	wg.Add(1)
	go func() { defer wg.Done(); <-runctx.Done(); s.Close(); stop() }()
	// The enrolled controller identity is checked before accepting any open.
	_ = s.WS.SetReadDeadline(time.Now().Add(8 * time.Second))
	hello, e := s.Read()
	if e != nil || hello != (Message{Type: "controller", Data: controllerID}) {
		return fmt.Errorf("console controller identity mismatch")
	}
	if e = s.Send(Message{Type: "hello", User: username}); e != nil {
		return e
	}
	for {
		_ = s.WS.SetReadDeadline(time.Now().Add(30 * time.Second))
		m, e := s.Read()
		if e != nil {
			return e
		}
		if m.Type == "ping" {
			if m.Session != "" || m.Data != "" || m.Ticket != "" || m.Seq != 0 || m.User != "" || m.Message != "" || m.Cols != 0 || m.Rows != 0 {
				return fmt.Errorf("invalid ping")
			}
			if e = s.Send(Message{Type: "pong"}); e != nil {
				return e
			}
			continue
		}
		if m.Type == "open" {
			if opened {
				return fmt.Errorf("a terminal permit is single-use per channel")
			}
			opened = true
			if !ID(m.Session) || !Size(m.Cols, m.Rows) || m.Seq != 0 || m.Ticket != "" || m.Data != "" || m.User != "" || m.Message != "" {
				return fmt.Errorf("invalid console permit")
			}
			mu.Lock()
			busy := active != nil
			mu.Unlock()
			if busy {
				return fmt.Errorf("second console forbidden")
			}
			c, e := dialLocal(runctx)
			if e != nil {
				_ = s.Send(Message{Type: "closed", Session: m.Session, Message: "Локальная консоль недоступна."})
				continue
			}
			l := NewLocal(c)
			mu.Lock()
			active = l
			activeID = m.Session
			lastSeq = 0
			mu.Unlock()
			if l.Send(m) != nil {
				stop()
				return fmt.Errorf("local console start failed")
			}
			wg.Add(1)
			go func(l *Local, id string) {
				defer wg.Done()
				defer l.Close()
				defer func() {
					mu.Lock()
					if active == l {
						active = nil
						activeID = ""
					}
					mu.Unlock()
					_ = s.Send(Message{Type: "closed", Session: id, Message: "Сеанс завершён. Повторное подключение вручную."})
				}()
				total := 0
				ready := false
				for {
					msg, e := l.Read()
					if e != nil {
						return
					}
					if msg.Session != id {
						return
					}
					switch msg.Type {
					case "ready":
						if ready || msg.User != username || msg.Ticket != "" || msg.Data != "" || msg.Message != "" || msg.Seq != 0 || msg.Cols != 0 || msg.Rows != 0 {
							return
						}
						ready = true
					case "output":
						if !ready {
							return
						}
						b, e := OutputBytes(msg)
						if e != nil {
							return
						}
						total += len(b)
						if total > OutputBudget {
							return
						}
					default:
						return
					}
					if s.Send(msg) != nil {
						cancel()
						return
					}
				}
			}(l, m.Session)
			continue
		}
		mu.Lock()
		l, id, last := active, activeID, lastSeq
		if l != nil && m.Session == id && ValidateInput(m, last) {
			lastSeq = m.Seq
		} else {
			l = nil
		}
		mu.Unlock()
		// A terminal can exit between a user keystroke and receipt. Do not replay it
		// into any replacement session. Stale session input is simply connection-fatal.
		if l == nil {
			return fmt.Errorf("inactive or invalid console frame")
		}
		if l.Send(m) != nil {
			stop()
			return fmt.Errorf("local console closed")
		}
		if m.Type == "close" {
			stop()
		}
	}
}
func LocalUser(ctx context.Context, dial func(context.Context) (net.Conn, error)) (string, error) {
	c, e := dial(ctx)
	if e != nil {
		return "", e
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(3 * time.Second))
	l := NewLocal(c)
	if e = l.Send(Message{Type: "info"}); e != nil {
		return "", e
	}
	m, e := l.Read()
	if e != nil || m.Type != "info" || m.User == "" || len(m.User) > 32 || m != (Message{Type: "info", User: m.User}) {
		return "", fmt.Errorf("local console policy unavailable")
	}
	return m.User, nil
}
