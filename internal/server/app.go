package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/clock"
	"github.com/alinescafs3mp-afk/monik_v2/internal/idgen"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"github.com/alinescafs3mp-afk/monik_v2/internal/secure"
	"github.com/alinescafs3mp-afk/monik_v2/internal/storage"
	"github.com/alinescafs3mp-afk/monik_v2/internal/tlsutil"
	"github.com/alinescafs3mp-afk/monik_v2/internal/version"
	"github.com/alinescafs3mp-afk/monik_v2/internal/webui"
)

type Config struct {
	DataDir        string
	Listen         string
	AdvertisedURL  string
	AdminAllowlist []string
	Clock          clock.Clock
	Logger         *slog.Logger
	RestoreMode    bool
}

type App struct {
	Cfg      Config
	Store    *storage.Store
	TLS      *tlsutil.Bundle
	Log      *slog.Logger
	Clock    clock.Clock
	Master   []byte
	HTTP     *http.Server
	mu       sync.Mutex
	limiters sync.Map
}

func Open(cfg Config) (*App, error) {
	if cfg.Clock == nil {
		cfg.Clock = clock.Real{}
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.NewJSONHandler(os.Stdout, nil))
	}
	if cfg.DataDir == "" {
		cfg.DataDir = filepath.Join(os.Getenv("HOME"), ".local", "share", "monik-server")
	}
	if cfg.Listen == "" {
		cfg.Listen = protocol.DefaultListen
	}
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return nil, err
	}
	st, err := storage.Open(filepath.Join(cfg.DataDir, "monik.db"), cfg.Clock)
	if err != nil {
		return nil, err
	}
	master, err := secure.LoadOrCreateKey(filepath.Join(cfg.DataDir, "secret-master.key"), 32)
	if err != nil {
		return nil, err
	}
	a := &App{Cfg: cfg, Store: st, Log: cfg.Logger, Clock: cfg.Clock, Master: master}
	if v, err := st.Setting("advertised_url"); err == nil {
		a.Cfg.AdvertisedURL = v
	}
	if v, err := st.Setting("listen"); err == nil && v != "" {
		a.Cfg.Listen = v
	}
	if v, err := st.Setting("controller_id"); err != nil {
		_ = st.SetSetting("controller_id", idgen.New())
	} else {
		_ = v
	}
	if _, err := st.Setting("restore_mode"); err == nil && st.MustSetting("restore_mode", "") == "1" {
		a.Cfg.RestoreMode = true
	}
	return a, nil
}

func (a *App) Close() error { return a.Store.Close() }

func (a *App) ControllerID() string {
	return a.Store.MustSetting("controller_id", "")
}

func (a *App) SetupComplete() bool {
	n, _ := a.Store.UserCount()
	return n > 0
}

type SetupRequest struct {
	Username      string   `json:"username"`
	Password      string   `json:"password"`
	AdvertisedURL string   `json:"advertised_url"`
	Listen        string   `json:"listen"`
	SANs          []string `json:"sans"`
}

func (a *App) CompleteSetup(req SetupRequest) error {
	if a.SetupComplete() {
		return fmt.Errorf("setup already completed")
	}
	if req.Username == "" || req.Password == "" {
		return fmt.Errorf("username and password required")
	}
	if len(req.Password) < 10 {
		return fmt.Errorf("password must be at least 10 characters")
	}
	hash, err := secure.HashPassword(req.Password)
	if err != nil {
		return err
	}
	if _, err := a.Store.CreateUser(req.Username, hash, "owner"); err != nil {
		return err
	}
	if req.AdvertisedURL == "" {
		req.AdvertisedURL = protocol.DefaultBootstrapURL
	}
	if req.Listen == "" {
		req.Listen = protocol.DefaultListen
	}
	_ = a.Store.SetSetting("advertised_url", req.AdvertisedURL)
	_ = a.Store.SetSetting("listen", req.Listen)
	a.Cfg.AdvertisedURL = req.AdvertisedURL
	a.Cfg.Listen = req.Listen
	a.Store.Audit(req.Username, "setup", "server", "initial owner created")
	return a.ensureTLS(req.SANs)
}

func (a *App) ensureTLS(extra []string) error {
	dir := filepath.Join(a.Cfg.DataDir, "tls")
	dns := []string{"localhost"}
	ips := []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}
	if a.Cfg.AdvertisedURL != "" {
		host := hostOf(a.Cfg.AdvertisedURL)
		if ip := net.ParseIP(host); ip != nil {
			ips = append(ips, ip)
		} else if host != "" {
			dns = append(dns, host)
		}
	}
	if host, _, err := net.SplitHostPort(a.Cfg.Listen); err == nil {
		if ip := net.ParseIP(host); ip != nil && !ip.IsUnspecified() {
			ips = append(ips, ip)
		}
	}
	ifaces, _ := net.InterfaceAddrs()
	for _, ia := range ifaces {
		if n, ok := ia.(*net.IPNet); ok && n.IP != nil && !n.IP.IsLoopback() {
			ips = append(ips, n.IP)
		}
	}
	for _, s := range extra {
		if ip := net.ParseIP(s); ip != nil {
			ips = append(ips, ip)
		} else if s != "" {
			dns = append(dns, s)
		}
	}
	b, err := tlsutil.LoadOrCreate(dir, dns, ips, 90*24*time.Hour)
	if err != nil {
		return err
	}
	a.TLS = b
	return nil
}

func hostOf(raw string) string {
	raw = strings.TrimPrefix(strings.TrimPrefix(raw, "https://"), "http://")
	if h, _, err := net.SplitHostPort(raw); err == nil {
		return h
	}
	return strings.TrimSuffix(raw, "/")
}

func (a *App) Run(ctx context.Context) error {
	if !a.SetupComplete() {
		a.Log.Info("setup required; UI will run local setup wizard")
	}
	if err := a.ensureTLS(nil); err != nil {
		return err
	}
	mux := http.NewServeMux()
	a.routes(mux)
	a.HTTP = &http.Server{
		Addr:              a.Cfg.Listen,
		Handler:           a.csrfAndSecurity(mux),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 16,
		TLSConfig:         a.TLS.TLSConfig(),
		ErrorLog:          slog.NewLogLogger(a.Log.Handler(), slog.LevelWarn),
	}
	go a.background(ctx)
	ln, err := net.Listen("tcp", a.Cfg.Listen)
	if err != nil {
		return err
	}
	a.Log.Info("monik-server listening", "addr", a.Cfg.Listen, "version", version.Version, "advertised", a.Cfg.AdvertisedURL)
	go func() {
		<-ctx.Done()
		shctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		_ = a.HTTP.Shutdown(shctx)
	}()
	return a.HTTP.ServeTLS(ln, "", "")
}

func (a *App) background(ctx context.Context) {
	t := a.Clock.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C():
			_ = a.Store.RetainRaw(protocol.RawRetention)
			_ = a.TLS.MaybeRenew(filepath.Join(a.Cfg.DataDir, "tls"), nil, nil, 90*24*time.Hour)
			a.refreshAgentStates()
		}
	}
}

func (a *App) refreshAgentStates() {
	agents, err := a.Store.Agents()
	if err != nil {
		return
	}
	now := a.Clock.Now()
	for _, ag := range agents {
		st, reason := "unknown", "no live report yet"
		if ag.LastLiveAt != nil {
			age := now.Sub(*ag.LastLiveAt)
			if age > protocol.UnreachableContact {
				st, reason = "unreachable", "no live report for 30s"
			} else if age > protocol.StaleContact {
				st, reason = "stale", "no live report for 15s"
			} else {
				st, reason = "ok", ""
			}
		}
		_ = a.Store.SetState("agent", ag.ID, st, reason)
	}
}

func (a *App) writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (a *App) writeErr(w http.ResponseWriter, status int, code, msg string) {
	a.writeJSON(w, status, map[string]any{"error": code, "message": msg})
}

func hashBody(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func (a *App) allowAdmin(r *http.Request) bool {
	if !a.SetupComplete() {
		host, _, _ := net.SplitHostPort(r.RemoteAddr)
		ip := net.ParseIP(host)
		return ip != nil && ip.IsLoopback()
	}
	return true
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (a *App) rateLimit(key string, n int, window time.Duration) bool {
	type bucket struct {
		n    int
		from time.Time
	}
	now := a.Clock.Now()
	v, _ := a.limiters.LoadOrStore(key, &bucket{from: now})
	b := v.(*bucket)
	a.mu.Lock()
	defer a.mu.Unlock()
	if now.Sub(b.from) > window {
		b.n = 0
		b.from = now
	}
	b.n++
	return b.n <= n
}

func (a *App) csrfAndSecurity(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self'; connect-src 'self'; frame-ancestors 'none'")
		if r.Method == http.MethodTrace {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if r.ContentLength > 32<<20 {
			a.writeErr(w, http.StatusRequestEntityTooLarge, "too_large", "request too large")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *App) serveUI(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/health" {
		a.writeJSON(w, 200, map[string]any{"ok": true, "setup_required": !a.SetupComplete(), "version": version.Version})
		return
	}
	webui.Serve(w, r)
}

func (a *App) CACertPEM() []byte {
	if a.TLS == nil {
		return nil
	}
	return a.TLS.CACertPEM
}
