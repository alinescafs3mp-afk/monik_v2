package setup

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/alinescafs3mp-afk/monik_v2/internal/agent/configfile"
	"github.com/alinescafs3mp-afk/monik_v2/internal/idgen"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"github.com/alinescafs3mp-afk/monik_v2/internal/tlsutil"
	"github.com/alinescafs3mp-afk/monik_v2/internal/version"
	"gopkg.in/yaml.v3"
)

type Profile struct {
	ControllerURL  string `json:"controller_url" yaml:"controller_url"`
	CACertPEM      string `json:"ca_cert_pem" yaml:"ca_cert_pem"`
	EnrollmentCode string `json:"enrollment_code" yaml:"enrollment_code"`
	DisplayName    string `json:"display_name" yaml:"display_name"`
	StateDir       string `json:"state_dir" yaml:"state_dir"`
	ConfigPath     string `json:"config_path" yaml:"config_path"`
}

func LoadProfile(path string) (*Profile, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	p := &Profile{}
	if json.Unmarshal(b, p) != nil {
		if err := yaml.Unmarshal(b, p); err != nil {
			return nil, err
		}
	}
	return p, nil
}

func Interactive(in io.Reader, out io.Writer) (*Profile, error) {
	rd := bufio.NewReader(in)
	fmt.Fprintf(out, "Адрес контроллера [%s]: ", protocol.DefaultBootstrapURL)
	line, _ := rd.ReadString('\n')
	url := strings.TrimSpace(line)
	if url == "" {
		url = protocol.DefaultBootstrapURL
	}
	fmt.Fprint(out, "Код регистрации (не отображается при вставке в профиль): ")
	code, _ := rd.ReadString('\n')
	fmt.Fprint(out, "Отображаемое имя (необязательно): ")
	name, _ := rd.ReadString('\n')
	return &Profile{ControllerURL: url, EnrollmentCode: strings.TrimSpace(code), DisplayName: strings.TrimSpace(name)}, nil
}

func Enroll(p *Profile) (*configfile.State, error) {
	if p.ControllerURL == "" {
		p.ControllerURL = protocol.DefaultBootstrapURL
	}
	if p.EnrollmentCode == "" {
		return nil, fmt.Errorf("enrollment code required")
	}
	if !strings.HasPrefix(p.ControllerURL, "https://") {
		return nil, fmt.Errorf("remote enrollment requires https")
	}
	if p.StateDir == "" {
		p.StateDir = defaultStateDir()
	}
	if err := os.MkdirAll(p.StateDir, 0o700); err != nil {
		return nil, err
	}
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if p.CACertPEM != "" {
		pool, err := tlsutil.PoolFromPEM([]byte(p.CACertPEM))
		if err != nil {
			return nil, err
		}
		tlsCfg.RootCAs = pool
	}
	host := hostOf(p.ControllerURL)
	if ip := net.ParseIP(host); ip != nil {
		tlsCfg.ServerName = ip.String()
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: tlsCfg, Proxy: nil}}
	agentID := idgen.New()
	hn, _ := os.Hostname()
	body, _ := json.Marshal(protocol.EnrollRequest{
		Code: p.EnrollmentCode, DisplayName: p.DisplayName, Hostname: hn,
		OS: runtime.GOOS, Arch: runtime.GOARCH, AgentID: agentID, Version: version.Version,
	})
	resp, err := client.Post(strings.TrimRight(p.ControllerURL, "/")+"/api/v1/agent/enroll", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("enroll: %w (TLS must verify; never skip)", err)
	}
	defer resp.Body.Close()
	slurp, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("enroll failed: %s %s", resp.Status, slurp)
	}
	var er protocol.EnrollResponse
	if err := json.Unmarshal(slurp, &er); err != nil {
		return nil, err
	}
	cfgPath := p.ConfigPath
	if cfgPath == "" {
		cfgPath = filepath.Join(p.StateDir, "agent.json")
	}
	credPath := filepath.Join(p.StateDir, "agent.credential")
	if err := configfile.WriteCredential(credPath, er.Credential); err != nil {
		return nil, err
	}
	st := &configfile.State{}
	st.File = configfile.File{
		SchemaVersion: protocol.SchemaVersion, AgentID: er.AgentID, DisplayName: p.DisplayName,
		ControllerURL: er.AdvertisedURL, ControllerID: er.ControllerID, CACertPEM: er.CACertPEM,
		CredentialPath: credPath, StateDir: p.StateDir, EndpointGeneration: er.EndpointGeneration,
		AppliedRevision: er.ConfigRevision, BootstrapURL: protocol.DefaultBootstrapURL,
	}
	if st.File.ControllerURL == "" {
		st.File.ControllerURL = p.ControllerURL
	}
	if st.File.CACertPEM == "" {
		st.File.CACertPEM = p.CACertPEM
	}
	// reuse Save via dummy path
	raw, _ := json.MarshalIndent(st.File, "", "  ")
	if err := os.WriteFile(cfgPath, raw, 0o600); err != nil {
		return nil, err
	}
	loaded, err := configfile.Load(cfgPath)
	if err != nil {
		return nil, err
	}
	return loaded, nil
}

func defaultStateDir() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(os.Getenv("PROGRAMDATA"), "Monik", "agent")
	}
	if os.Geteuid() == 0 {
		return "/var/lib/monik-agent"
	}
	return filepath.Join(os.Getenv("HOME"), ".local", "share", "monik-agent")
}

func hostOf(raw string) string {
	raw = strings.TrimPrefix(strings.TrimPrefix(raw, "https://"), "http://")
	if h, _, err := net.SplitHostPort(raw); err == nil {
		return h
	}
	return strings.TrimSuffix(raw, "/")
}

func DefaultConfigPath() string {
	return filepath.Join(defaultStateDir(), "agent.json")
}
