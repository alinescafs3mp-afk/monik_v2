package tufutil

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/theupdateframework/go-tuf/v2/metadata"
)

type HighWater struct {
	Root      int64 `json:"root"`
	Timestamp int64 `json:"timestamp"`
	Snapshot  int64 `json:"snapshot"`
	Targets   int64 `json:"targets"`
}

type ImportOpts struct {
	// TrustedRoot is the independently enrolled TUF root. Required unless Enroll is set
	// and no root has been stored yet.
	TrustedRoot []byte
	Enroll      bool
	Now         time.Time
}

func TrustedRootPath(tufDir string) string {
	return filepath.Join(tufDir, "trusted", "root.json")
}

func HighWaterPath(tufDir string) string {
	return filepath.Join(tufDir, "trusted", "versions.json")
}

func LoadTrustedRoot(tufDir string) ([]byte, error) {
	b, err := os.ReadFile(TrustedRootPath(tufDir))
	if err != nil {
		return nil, err
	}
	if len(b) == 0 {
		return nil, fmt.Errorf("enrolled TUF root is empty")
	}
	return b, nil
}

func SaveTrustedRoot(tufDir string, root []byte) error {
	if err := os.MkdirAll(filepath.Join(tufDir, "trusted"), 0o700); err != nil {
		return err
	}
	tmp := TrustedRootPath(tufDir) + ".tmp"
	if err := os.WriteFile(tmp, root, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, TrustedRootPath(tufDir))
}

func LoadHighWater(tufDir string) HighWater {
	b, err := os.ReadFile(HighWaterPath(tufDir))
	if err != nil {
		return HighWater{}
	}
	var h HighWater
	_ = json.Unmarshal(b, &h)
	return h
}

func saveHighWater(tufDir string, h HighWater) error {
	if err := os.MkdirAll(filepath.Join(tufDir, "trusted"), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(h, "", "  ")
	if err != nil {
		return err
	}
	tmp := HighWaterPath(tufDir) + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, HighWaterPath(tufDir))
}

func parseRoot(raw []byte) (*metadata.Metadata[metadata.RootType], error) {
	root := metadata.Root()
	if _, err := root.FromBytes(raw); err != nil {
		return nil, fmt.Errorf("invalid TUF root: %w", err)
	}
	if root.Signed.Type != metadata.ROOT {
		return nil, fmt.Errorf("trusted file is not a TUF root")
	}
	return root, nil
}

func rootKeyFingerprint(root *metadata.Metadata[metadata.RootType]) string {
	ids := make([]string, 0, len(root.Signed.Keys))
	for id := range root.Signed.Keys {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return strings.Join(ids, ",")
}

func loadMeta[T metadata.Roles](path string, empty *metadata.Metadata[T]) (*metadata.Metadata[T], error) {
	if _, err := empty.FromFile(path); err != nil {
		return nil, err
	}
	return empty, nil
}

func verifyExpiry(expires time.Time, now time.Time, role string) error {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if !expires.After(now) {
		return fmt.Errorf("%s metadata expired at %s", role, expires.UTC().Format(time.RFC3339))
	}
	return nil
}

func rejectReplay(role string, got, have int64) error {
	if have > 0 && got < have {
		return fmt.Errorf("%s metadata version %d is older than enrolled high-water %d", role, got, have)
	}
	return nil
}

// VerifyRepo checks timestamp/snapshot/targets in repoDir against an independently
// enrolled root. It does not trust a root that arrived in the same untrusted bundle.
func VerifyRepo(trustedRoot []byte, repoDir string, hw HighWater, now time.Time) (HighWater, error) {
	root, err := parseRoot(trustedRoot)
	if err != nil {
		return hw, err
	}
	if err := verifyExpiry(root.Signed.Expires, now, "root"); err != nil {
		return hw, err
	}
	if err := rejectReplay("root", root.Signed.Version, hw.Root); err != nil {
		return hw, err
	}
	ts, err := loadMeta(filepath.Join(repoDir, "timestamp.json"), metadata.Timestamp())
	if err != nil {
		return hw, fmt.Errorf("timestamp: %w", err)
	}
	if err := root.VerifyDelegate("timestamp", ts); err != nil {
		return hw, fmt.Errorf("timestamp signature: %w", err)
	}
	if err := verifyExpiry(ts.Signed.Expires, now, "timestamp"); err != nil {
		return hw, err
	}
	if err := rejectReplay("timestamp", ts.Signed.Version, hw.Timestamp); err != nil {
		return hw, err
	}
	sn, err := loadMeta(filepath.Join(repoDir, "snapshot.json"), metadata.Snapshot())
	if err != nil {
		return hw, fmt.Errorf("snapshot: %w", err)
	}
	if err := root.VerifyDelegate("snapshot", sn); err != nil {
		return hw, fmt.Errorf("snapshot signature: %w", err)
	}
	if err := verifyExpiry(sn.Signed.Expires, now, "snapshot"); err != nil {
		return hw, err
	}
	if err := rejectReplay("snapshot", sn.Signed.Version, hw.Snapshot); err != nil {
		return hw, err
	}
	tg, err := loadMeta(filepath.Join(repoDir, "targets.json"), metadata.Targets())
	if err != nil {
		return hw, fmt.Errorf("targets: %w", err)
	}
	if err := root.VerifyDelegate("targets", tg); err != nil {
		return hw, fmt.Errorf("targets signature: %w", err)
	}
	if err := verifyExpiry(tg.Signed.Expires, now, "targets"); err != nil {
		return hw, err
	}
	if err := rejectReplay("targets", tg.Signed.Version, hw.Targets); err != nil {
		return hw, err
	}
	out := HighWater{
		Root:      root.Signed.Version,
		Timestamp: ts.Signed.Version,
		Snapshot:  sn.Signed.Version,
		Targets:   tg.Signed.Version,
	}
	if hw.Root > out.Root {
		out.Root = hw.Root
	}
	return out, nil
}

func existingVersion(path string, role string) int64 {
	switch role {
	case "timestamp":
		m := metadata.Timestamp()
		if _, err := m.FromFile(path); err == nil {
			return m.Signed.Version
		}
	case "snapshot":
		m := metadata.Snapshot()
		if _, err := m.FromFile(path); err == nil {
			return m.Signed.Version
		}
	case "targets":
		m := metadata.Targets()
		if _, err := m.FromFile(path); err == nil {
			return m.Signed.Version
		}
	case "root":
		m := metadata.Root()
		if _, err := m.FromFile(path); err == nil {
			return m.Signed.Version
		}
	}
	return 0
}
