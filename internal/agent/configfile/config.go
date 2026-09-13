package configfile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"github.com/alinescafs3mp-afk/monik_v2/internal/secure"
)

type File struct {
	SchemaVersion      int    `json:"schema_version"`
	AgentID            string `json:"agent_id"`
	DisplayName        string `json:"display_name"`
	ControllerURL      string `json:"controller_url"`
	ControllerID       string `json:"controller_id"`
	CACertPEM          string `json:"ca_cert_pem"`
	CredentialPath     string `json:"credential_path"`
	StateDir           string `json:"state_dir"`
	EndpointGeneration int64  `json:"endpoint_generation"`
	AppliedRevision    int64  `json:"applied_revision"`
	AppliedHash        string `json:"applied_hash"`
	UpdateRootPath     string `json:"update_root_path,omitempty"`
	Managed            bool   `json:"managed"`
	BootstrapURL       string `json:"bootstrap_url"`
}

type State struct {
	mu   sync.Mutex
	path string
	File File
}

func Load(path string) (*State, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f File
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, err
	}
	return &State{path: path, File: f}, nil
}

func (s *State) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	b, err := json.MarshalIndent(s.File, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func WriteCredential(path, cred string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(cred), 0o600)
}

func ReadCredential(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func SaveAppliedConfig(dir string, cfg protocol.AgentConfig, rev int64, hash string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(cfg, "", "  ")
	tmp := filepath.Join(dir, "applied.json.tmp")
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, "applied.json"))
}

func LoadAppliedConfig(dir string) (protocol.AgentConfig, error) {
	b, err := os.ReadFile(filepath.Join(dir, "applied.json"))
	if err != nil {
		return protocol.DefaultAgentConfig(), err
	}
	var c protocol.AgentConfig
	err = json.Unmarshal(b, &c)
	return c, err
}

func HashConfig(c protocol.AgentConfig) string {
	b, _ := json.Marshal(c)
	return secure.SHA256Bytes(b)
}

func SaveMigration(dir string, plan *protocol.MigrationPlan) error {
	if plan == nil {
		return nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, "migration.json.tmp")
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, "migration.json"))
}

func LoadMigration(dir string) (*protocol.MigrationPlan, error) {
	b, err := os.ReadFile(filepath.Join(dir, "migration.json"))
	if err != nil {
		return nil, err
	}
	p := &protocol.MigrationPlan{}
	if err := json.Unmarshal(b, p); err != nil {
		return nil, err
	}
	return p, nil
}

func ClearMigration(dir string) error {
	err := os.Remove(filepath.Join(dir, "migration.json"))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
