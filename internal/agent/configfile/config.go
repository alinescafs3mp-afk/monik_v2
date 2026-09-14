package configfile

import (
	"encoding/json"
	"fmt"
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
	b, err := json.MarshalIndent(s.File, "", "  ")
	if err != nil {
		return err
	}
	return secure.AtomicWrite(s.path, b, 0600)
}

func WriteCredential(path, cred string) error { return secure.AtomicWrite(path, []byte(cred), 0600) }

func ReadCredential(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func SaveAppliedConfig(dir string, cfg protocol.AgentConfig, rev int64, hash string) error {
	if hash != HashConfig(cfg) || rev < 0 {
		return fmt.Errorf("invalid applied configuration identity")
	}
	b, err := json.MarshalIndent(protocol.DesiredConfig{Revision: rev, Hash: hash, Body: cfg}, "", "  ")
	if err != nil {
		return err
	}
	if err := secure.AtomicWrite(filepath.Join(dir, "applied-envelope.json"), b, 0600); err != nil {
		return err
	}
	// Preserve the old plain-body file for a previous worker during rollback.
	legacy, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return secure.AtomicWrite(filepath.Join(dir, "applied.json"), legacy, 0600)
}

// LoadAppliedEnvelope also reads the original plain-body format, whose revision
// is supplied by the caller from the legacy identity file. New writes are whole.
func LoadAppliedEnvelope(dir string) (protocol.DesiredConfig, error) {
	var result protocol.DesiredConfig
	b, err := os.ReadFile(filepath.Join(dir, "applied-envelope.json"))
	if os.IsNotExist(err) {
		b, err = os.ReadFile(filepath.Join(dir, "applied.json"))
	}
	if err != nil {
		return result, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(b, &fields); err != nil {
		return result, err
	}
	if _, ok := fields["body"]; ok {
		if err := json.Unmarshal(b, &result); err != nil {
			return result, err
		}
		if result.Hash != HashConfig(result.Body) || result.Revision < 0 {
			return result, fmt.Errorf("applied config journal hash/revision mismatch")
		}
	} else {
		if err := json.Unmarshal(b, &result.Body); err != nil {
			return result, err
		}
		result.Hash = HashConfig(result.Body)
		result.Revision = -1
	}
	if err := protocol.ValidateAgentConfig(result.Body); err != nil {
		return result, err
	}
	return result, nil
}
func LoadAppliedConfig(dir string) (protocol.AgentConfig, error) {
	d, err := LoadAppliedEnvelope(dir)
	return d.Body, err
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
	return secure.AtomicWrite(filepath.Join(dir, "migration.json"), b, 0600)
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
