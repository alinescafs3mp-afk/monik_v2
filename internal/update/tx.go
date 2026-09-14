package update

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/alinescafs3mp-afk/monik_v2/internal/secure"
	"io"
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
	info, err := os.Lstat(Path(stateDir))
	if err == nil && (!info.Mode().IsRegular() || info.Size() > 64<<10) {
		return nil, fmt.Errorf("invalid update journal size/type")
	}
	if err != nil {
		if os.IsNotExist(err) {
			return &Journal{Stage: StageIdle}, nil
		}
		return nil, err
	}
	f, err := os.Open(Path(stateDir))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, (64<<10)+1))
	if len(b) > 64<<10 {
		return nil, fmt.Errorf("update journal exceeds limit")
	}
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
	switch j.Stage {
	case StageIdle, StageStaged, StageSwitching, StageProbation, StageConfirmed, StageRollback:
	default:
		return nil, fmt.Errorf("unknown or missing update journal stage; restore verified state before starting")
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
	return secure.AtomicWrite(Path(stateDir), b, 0600)
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
	absState, err := filepath.EvalSymlinks(stateDir)
	if err != nil {
		return err
	}
	absPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(absState, absPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
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
	info, err := os.Stat(src)
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("artifact is not a regular file")
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
	if err := atomicBinary(dst, b); err != nil {
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

// Keep the current executable in place until the complete candidate is ready.
// A failed backup or write must never remove the last runnable version.
func Activate(current, staged, prev string) error {
	candidate, err := os.ReadFile(staged)
	if err != nil {
		return err
	}
	old, err := os.ReadFile(current)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(prev), 0o700); err != nil {
		return err
	}
	if err := atomicBinary(prev, old); err != nil {
		return err
	}
	return atomicBinary(current, candidate)
}

func atomicBinary(path string, b []byte) error { return secure.AtomicWrite(path, b, 0755) }

func Rollback(current, prev string) error {
	if _, err := os.Stat(prev); err != nil {
		return fmt.Errorf("no previous binary to restore")
	}
	b, err := os.ReadFile(prev)
	if err != nil {
		return err
	}
	return atomicBinary(current, b)
}

// RestoreVerified binds validation to the exact bytes published, not to a
// separate pre-read hash followed by an unchecked second read of the slot.
func RestoreVerified(current, previous, digest string) error {
	expected, err := hex.DecodeString(digest)
	if err != nil || len(expected) != sha256.Size {
		return fmt.Errorf("previous-good digest is required")
	}
	b, err := os.ReadFile(previous)
	if err != nil {
		return err
	}
	hash := sha256.Sum256(b)
	if hex.EncodeToString(hash[:]) != digest {
		return fmt.Errorf("previous-good slot digest mismatch")
	}
	return atomicBinary(current, b)
}
