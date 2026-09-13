package update

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Stage string

const (
	StageIdle      Stage = "idle"
	StageStaged    Stage = "staged"
	StageSwitching Stage = "switching"
	StageProbation Stage = "local_probation"
	StageConfirmed Stage = "confirmed"
	StageRollback  Stage = "rolled_back"
)

type Journal struct {
	TxID      string    `json:"tx_id"`
	Stage     Stage     `json:"stage"`
	OldPath   string    `json:"old_path"`
	NewPath   string    `json:"new_path"`
	Current   string    `json:"current_path"`
	PrevPath  string    `json:"prev_path"`
	OldSHA    string    `json:"old_sha256"`
	NewSHA    string    `json:"new_sha256"`
	StartedAt time.Time `json:"started_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Reason    string    `json:"reason,omitempty"`
}

func Path(stateDir string) string { return filepath.Join(stateDir, "update-journal.json") }

func Load(stateDir string) (*Journal, error) {
	b, err := os.ReadFile(Path(stateDir))
	if err != nil {
		if os.IsNotExist(err) {
			return &Journal{Stage: StageIdle}, nil
		}
		return nil, err
	}
	j := &Journal{}
	if err := json.Unmarshal(b, j); err != nil {
		return nil, err
	}
	return j, nil
}

func (j *Journal) Save(stateDir string) error {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return err
	}
	j.UpdatedAt = time.Now().UTC()
	b, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return err
	}
	tmp := Path(stateDir) + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, Path(stateDir))
}

func SHA256File(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

func confined(stateDir, path string) error {
	absState, err := filepath.Abs(stateDir)
	if err != nil {
		return err
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(absState, absPath)
	if err != nil || strings.HasPrefix(rel, "..") {
		return fmt.Errorf("artifact path is outside the protected state directory")
	}
	return nil
}

func StageBinary(stateDir, src, expectSHA string) (staged string, err error) {
	if expectSHA == "" {
		return "", fmt.Errorf("sha256 is required")
	}
	if err := confined(stateDir, src); err != nil {
		return "", err
	}
	sum, err := SHA256File(src)
	if err != nil {
		return "", err
	}
	if sum != expectSHA {
		return "", fmt.Errorf("artifact sha256 mismatch")
	}
	dir := filepath.Join(stateDir, "updates")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	dst := filepath.Join(dir, "next.bin")
	b, err := os.ReadFile(src)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(dst, b, 0o755); err != nil {
		return "", err
	}
	got, err := SHA256File(dst)
	if err != nil {
		return "", err
	}
	if got != sum {
		return "", fmt.Errorf("staged copy digest mismatch")
	}
	return dst, nil
}

func Activate(current, staged, prev string) error {
	if err := os.MkdirAll(filepath.Dir(prev), 0o700); err != nil {
		return err
	}
	if _, err := os.Stat(current); err == nil {
		_ = os.Remove(prev)
		if err := os.Rename(current, prev); err != nil {
			b, rerr := os.ReadFile(current)
			if rerr != nil {
				return err
			}
			if werr := os.WriteFile(prev, b, 0o755); werr != nil {
				return werr
			}
		}
	}
	b, err := os.ReadFile(staged)
	if err != nil {
		return err
	}
	tmp := current + ".new"
	if err := os.WriteFile(tmp, b, 0o755); err != nil {
		return err
	}
	return os.Rename(tmp, current)
}

func Rollback(current, prev string) error {
	if _, err := os.Stat(prev); err != nil {
		return fmt.Errorf("no previous binary to restore")
	}
	b, err := os.ReadFile(prev)
	if err != nil {
		return err
	}
	tmp := current + ".rb"
	if err := os.WriteFile(tmp, b, 0o755); err != nil {
		return err
	}
	return os.Rename(tmp, current)
}
