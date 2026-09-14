package setup

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"gopkg.in/yaml.v3"
)

type Profile struct {
	AutoDiscover   bool   `json:"auto_discover" yaml:"auto_discover"`
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
