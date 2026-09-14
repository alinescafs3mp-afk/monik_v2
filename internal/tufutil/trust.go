package tufutil

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/secure"
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
	return secure.AtomicWrite(TrustedRootPath(tufDir), root, 0600)
}

func SaveHighWater(tufDir string, h HighWater) error {
	if err := os.MkdirAll(filepath.Join(tufDir, "trusted"), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(h, "", "  ")
	if err != nil {
		return err
	}
	return secure.AtomicWrite(HighWaterPath(tufDir), b, 0600)
}

// ReadHighWater refuses corrupt state instead of silently resetting anti-rollback protection.
func ReadHighWater(tufDir string) (HighWater, error) {
	b, e := os.ReadFile(HighWaterPath(tufDir))
	if os.IsNotExist(e) {
		return HighWater{}, nil
	}
	if e != nil {
		return HighWater{}, e
	}
	return DecodeHighWater(b)
}
func DecodeHighWater(b []byte) (HighWater, error) {
	var h HighWater
	var fields map[string]json.RawMessage
	if len(b) > 4096 || json.Unmarshal(b, &fields) != nil || len(fields) != 4 {
		return h, fmt.Errorf("invalid TUF version journal object")
	}
	for _, k := range []string{"root", "timestamp", "snapshot", "targets"} {
		v, ok := fields[k]
		var n *int64
		if !ok || json.Unmarshal(v, &n) != nil || n == nil || *n < 0 {
			return h, fmt.Errorf("invalid TUF version field: %s", k)
		}
	}
	if e := json.Unmarshal(b, &h); e != nil {
		return h, e
	}
	return h, nil
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
	if err := verifyMetaLink(ts.Signed.Meta["snapshot.json"], sn.Signed.Version, filepath.Join(repoDir, "snapshot.json")); err != nil {
		return hw, fmt.Errorf("timestamp/snapshot link: %w", err)
	}
	if err := verifyMetaLink(sn.Signed.Meta["targets.json"], tg.Signed.Version, filepath.Join(repoDir, "targets.json")); err != nil {
		return hw, fmt.Errorf("snapshot/targets link: %w", err)
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

func verifyMetaLink(link *metadata.MetaFiles, actualVersion int64, path string) error {
	if link == nil || link.Version < 1 || link.Version != actualVersion {
		return fmt.Errorf("referenced metadata version mismatch")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return link.VerifyLengthHashes(b)
}

// VerifyTarget must be called only after VerifyRepo has authenticated this targets file.
// The command's digest is a consistency assertion, never the source of trust.
func VerifyTarget(repoDir, name string, raw []byte) error {
	if name == "" || strings.Contains(name, "\\") || strings.HasPrefix(name, "/") || strings.Contains(name, "..") {
		return fmt.Errorf("invalid target path")
	}
	tg := metadata.Targets()
	if _, err := tg.FromFile(filepath.Join(repoDir, "targets.json")); err != nil {
		return err
	}
	info, ok := tg.Signed.Targets[name]
	if !ok || info == nil {
		return fmt.Errorf("artifact is not in authenticated targets metadata")
	}
	return info.VerifyLengthHashes(raw)
}

// RepositoryExpiry is advisory until VerifyRepo has authenticated this set.
func RepositoryExpiry(rootRaw []byte, dir string) (time.Time, error) {
	r, e := parseRoot(rootRaw)
	if e != nil {
		return time.Time{}, e
	}
	expiry := r.Signed.Expires
	ts, e := loadMeta(filepath.Join(dir, "timestamp.json"), metadata.Timestamp())
	if e != nil {
		return expiry, e
	}
	sn, e := loadMeta(filepath.Join(dir, "snapshot.json"), metadata.Snapshot())
	if e != nil {
		return expiry, e
	}
	tg, e := loadMeta(filepath.Join(dir, "targets.json"), metadata.Targets())
	if e != nil {
		return expiry, e
	}
	for _, d := range []time.Time{ts.Signed.Expires, sn.Signed.Expires, tg.Signed.Expires} {
		if d.Before(expiry) {
			expiry = d
		}
	}
	return expiry, nil
}
