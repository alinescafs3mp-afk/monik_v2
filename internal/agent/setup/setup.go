package setup

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/alinescafs3mp-afk/monik_v2/internal/jsonutil"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"gopkg.in/yaml.v3"
)

type Profile struct {
	SchemaVersion  int    `json:"schema_version,omitempty" yaml:"schema_version,omitempty"`
	AutoDiscover   bool   `json:"auto_discover" yaml:"auto_discover"`
	ControllerURL  string `json:"controller_url" yaml:"controller_url"`
	CACertPEM      string `json:"ca_cert_pem" yaml:"ca_cert_pem"`
	EnrollmentCode string `json:"enrollment_code" yaml:"enrollment_code"`
	DisplayName    string `json:"display_name" yaml:"display_name"`
	StateDir       string `json:"state_dir" yaml:"state_dir"`
	ConfigPath     string `json:"config_path" yaml:"config_path"`
}

func LoadProfile(path string) (*Profile, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, (128<<10)+1))
	if err != nil {
		return nil, err
	}
	if len(b) > 128<<10 {
		return nil, fmt.Errorf("enrollment profile exceeds limit")
	}
	p := &Profile{}
	if bytes.HasPrefix(bytes.TrimSpace(b), []byte("{")) {
		// Malformed JSON must not fall through to a more permissive YAML parser.
		if err = jsonutil.Validate(b); err != nil {
			return nil, err
		}
		d := json.NewDecoder(bytes.NewReader(b))
		d.DisallowUnknownFields()
		if err = d.Decode(p); err != nil {
			return nil, fmt.Errorf("invalid enrollment JSON profile")
		}
	} else {
		var document yaml.Node
		if err = yaml.Unmarshal(b, &document); err != nil || len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
			return nil, fmt.Errorf("enrollment YAML must be a mapping")
		}
		d := yaml.NewDecoder(bytes.NewReader(b))
		d.KnownFields(true)
		if err = d.Decode(p); err != nil {
			return nil, fmt.Errorf("invalid enrollment YAML profile")
		}
		var extra any
		if err = d.Decode(&extra); err != io.EOF {
			return nil, fmt.Errorf("multiple enrollment documents are not accepted")
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
