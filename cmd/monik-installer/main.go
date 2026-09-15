// monik-installer is distributed as a single ELF with a bound enrollment profile
// and two verified executables. It installs the existing managed service; it is
// never a second implementation of the monitoring worker or update supervisor.
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/installerbundle"
	"github.com/alinescafs3mp-afk/monik_v2/internal/jsonutil"
)

const stateDir = "/var/lib/monik-agent"
const configPath = stateDir + "/agent.json"

type installedConfig struct {
	AgentID        string `json:"agent_id"`
	ControllerID   string `json:"controller_id"`
	ControllerURL  string `json:"controller_url"`
	CACertPEM      string `json:"ca_cert_pem"`
	CredentialPath string `json:"credential_path"`
	StateDir       string `json:"state_dir"`
	Pending        bool   `json:"pending_registration"`
	Managed        bool   `json:"managed"`
}
type installationStatus struct {
	Ready            bool   `json:"ready"`
	AgentID          string `json:"agent_id"`
	ControllerID     string `json:"controller_id"`
	SessionID        string `json:"session_id"`
	Seq              int64  `json:"seq"`
	WorkerDigest     string `json:"worker_digest"`
	SupervisorDigest string `json:"supervisor_digest"`
}

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "--help" || os.Args[1] == "-h") {
		fmt.Println("Monik: sudo ./monik-agent\nInstalls the managed Linux system service from this prepared file.\n--inspect: show non-secret package identity; no changes.\nDownload a separate prepared file for each new machine from Add machine.")
		return
	}
	if len(os.Args) > 2 || len(os.Args) == 2 && os.Args[1] != "--inspect" {
		fmt.Fprintln(os.Stderr, "Unknown argument; use --help")
		os.Exit(2)
	}
	path, err := os.Executable()
	if err != nil {
		fail(err)
	}
	f, err := os.Open(path)
	if err != nil {
		fail(err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		fail(err)
	}
	b, err := installerbundle.Open(f, info.Size())
	if err != nil {
		fail(err)
	}
	if len(os.Args) == 2 {
		fmt.Printf("Monik installer %s %s/%s\n", b.Manifest.Build, b.Manifest.OS, b.Manifest.Arch)
		if p := b.Manifest.Profile; p != nil {
			fmt.Printf("Controller: %s\nProfile expires: %s\nOne machine. Enrollment credential is intentionally not printed.\n", p.ControllerURL, p.ExpiresAt.UTC().Format(time.RFC3339))
		} else {
			fmt.Println("Template only. No enrollment profile.")
		}
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runNative(ctx, b, os.Stdout); err != nil {
		fail(err)
	}
}
func fail(err error) { fmt.Fprintln(os.Stderr, "Monik: НЕ ГОТОВО:", err); os.Exit(1) }

// These phases are injectable only in tests, never through flags or environment.
// The real adapter below always uses fixed protected paths and system tools.
type steps struct {
	preflight func() error
	load      func() (*installedConfig, error)
	active    func() bool
	setup     func(context.Context, *installerbundle.Bundle) (*installedConfig, error)
	install   func(context.Context, *installerbundle.Bundle, *installedConfig) error
	ready     func(context.Context, *installedConfig) error
	now       func() time.Time
}

func execute(ctx context.Context, b *installerbundle.Bundle, s steps, out io.Writer) error {
	fmt.Fprintln(out, "[1/4] Проверяем установочный файл и systemd…")
	if e := s.preflight(); e != nil {
		return e
	}
	existing, e := s.load()
	if e != nil {
		return e
	}
	if existing != nil {
		if p := b.Manifest.Profile; p != nil && existing.ControllerID != "" && existing.ControllerID != p.ControllerID {
			return fmt.Errorf("machine already belongs to a different controller; identity was not changed")
		}
		if existing.Pending {
			return fmt.Errorf("existing registration awaits owner approval; identity was preserved")
		}
		if existing.Managed && s.active() {
			fmt.Fprintln(out, "Агент уже установлен. Бинарники, адрес и идентичность не меняем.")
			if e := s.ready(ctx, existing); e != nil {
				return e
			}
			return printReady(out, existing)
		}
	}
	p := b.Manifest.Profile
	if existing == nil {
		if p == nil {
			return fmt.Errorf("download a prepared one-machine file from Add machine")
		}
		if !s.now().Before(p.ExpiresAt) {
			return fmt.Errorf("enrollment profile expired; download a fresh file; no installation was started")
		}
		fmt.Fprintln(out, "[2/4] Проверяем TLS и регистрируем эту машину…")
		existing, e = s.setup(ctx, b)
		if e != nil {
			return e
		}
	}
	fmt.Fprintln(out, "[3/4] Устанавливаем службу и автозапуск…")
	if e = s.install(ctx, b, existing); e != nil {
		return e
	}
	// Re-read managed identity after the native installer changes its local mode.
	existing, e = s.load()
	if e != nil {
		return e
	}
	if existing == nil || !existing.Managed {
		return fmt.Errorf("managed installation was not saved")
	}
	fmt.Fprintln(out, "[4/4] Ждём два свежих подтверждённых отчёта и связь с супервизором…")
	if e = s.ready(ctx, existing); e != nil {
		return e
	}
	return printReady(out, existing)
}
func printReady(out io.Writer, c *installedConfig) error {
	_, e := fmt.Fprintf(out, "ГОТОВО. Служба установлена и включена; контроллер подтвердил мониторинг.\nМашина: %s\nВеб-интерфейс: %s/machines/%s\nКонсоль можно закрыть. Повторная регистрация не нужна.\n", c.AgentID, strings.TrimRight(c.ControllerURL, "/"), c.AgentID)
	return e
}
func clientFor(ca string) *http.Client {
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM([]byte(ca))
	return &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}
func readResponse(ctx context.Context, client *http.Client, url, agentID, auth string, dst any) error {
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if e != nil {
		return fmt.Errorf("invalid controller address")
	}
	if auth != "" {
		req.Header.Set("Authorization", "Bearer "+auth)
		req.Header.Set("X-Monik-Agent-Id", agentID)
	}
	resp, e := client.Do(req)
	if e != nil {
		return fmt.Errorf("controller unreachable or TLS verification failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("controller returned HTTP %d (no success confirmed)", resp.StatusCode)
	}
	return jsonutil.ReadObject(resp.Body, 64<<10, dst)
}
func verifyController(ctx context.Context, p *installerbundle.Profile) error {
	client := clientFor(p.CACertPEM)
	defer client.CloseIdleConnections()
	var id struct {
		ControllerID string `json:"controller_id"`
		Product      string `json:"product"`
		Writable     bool   `json:"writable"`
	}
	if e := readResponse(ctx, client, strings.TrimRight(p.ControllerURL, "/")+"/api/v1/agent/identity", "", "", &id); e != nil {
		return e
	}
	if id.ControllerID != p.ControllerID || id.Product != "monik" || !id.Writable {
		return fmt.Errorf("controller identity mismatch or restore mode; no enrollment was attempted")
	}
	return nil
}
func encodeSetup(p *installerbundle.Profile) []byte {
	b, _ := json.Marshal(map[string]any{"controller_url": p.ControllerURL, "ca_cert_pem": p.CACertPEM, "enrollment_code": p.EnrollmentCode, "state_dir": stateDir, "config_path": configPath})
	return b
}
