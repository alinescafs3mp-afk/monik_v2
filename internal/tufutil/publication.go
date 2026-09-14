package tufutil

// The controller publishes a verified release into an immutable, content-addressed
// directory. This step never enrolls trust or commits the catalogue: the caller
// commits those together in SQLite only after every file has reached this directory.
import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/secure"
)

type PublicationFile struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Length int64  `json:"length"`
}
type Publication struct {
	ExpiresAt   time.Time
	Result      *BundleResult
	TrustedRoot []byte
	HighWater   HighWater
	Files       []PublicationFile
}

func ValidReleaseID(id string) bool {
	if len(id) != 64 || strings.ToLower(id) != id {
		return false
	}
	_, e := hex.DecodeString(id)
	return e == nil
}
func ValidTargetName(name string) bool {
	if name == "" || len(name) > 256 || strings.ContainsAny(name, "\\:%?#\x00") || strings.HasPrefix(name, "/") {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("/-._", r)) {
			return false
		}
	}
	return true
}
func PublicationDir(tufDir, id string) (string, error) {
	if !ValidReleaseID(id) {
		return "", fmt.Errorf("invalid release digest")
	}
	return filepath.Join(tufDir, "releases", id), nil
}
func publicationDigest(files []PublicationFile) string {
	b, _ := json.Marshal(files)
	return secure.SHA256Bytes(b)
}
func syncDir(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	return f.Sync()
}

func PreparePublication(bundlePath, tufDir string, opts ImportOpts, prior HighWater) (*Publication, error) {
	tmp, e := extractBundle(bundlePath)
	if e != nil {
		return nil, e
	}
	// extractBundle accepts both flat and repository/ archive layouts.
	cleanup := tmp
	if filepath.Base(tmp) == "repository" && strings.HasPrefix(filepath.Base(filepath.Dir(tmp)), "monik-import-") {
		cleanup = filepath.Dir(tmp)
	}
	defer os.RemoveAll(cleanup)
	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	trusted := opts.TrustedRoot
	incoming, e := os.ReadFile(filepath.Join(tmp, "root.json"))
	if e != nil {
		return nil, e
	}
	if len(trusted) == 0 {
		if !opts.Enroll {
			return nil, fmt.Errorf("no enrolled TUF root; explicitly enroll a first owner-trusted bundle")
		}
		root, e := parseRoot(incoming)
		if e != nil {
			return nil, e
		}
		if e = root.VerifyDelegate("root", root); e != nil {
			return nil, fmt.Errorf("root self-signature: %w", e)
		}
		trusted = incoming
	} else {
		root, e := parseRoot(trusted)
		if e != nil {
			return nil, e
		}
		candidate, e := parseRoot(incoming)
		if e != nil {
			return nil, e
		}
		if rootKeyFingerprint(root) != rootKeyFingerprint(candidate) {
			return nil, fmt.Errorf("root rotation requires a separately verified rotation flow")
		}
	}
	hw, e := VerifyRepo(trusted, tmp, prior, now)
	if e != nil {
		return nil, e
	}
	expiry, e := RepositoryExpiry(trusted, tmp)
	if e != nil {
		return nil, e
	}
	arts, plats, e := collectArtifacts(tmp)
	if e != nil {
		return nil, e
	}
	if len(arts) == 0 || len(arts) > 128 {
		return nil, fmt.Errorf("release must contain 1..128 signed targets")
	}
	res := layoutResult(tmp, arts, plats)
	if res.MetadataJSON == "" {
		res.MetadataJSON = "{}"
	}
	if !json.Valid([]byte(res.MetadataJSON)) {
		return nil, fmt.Errorf("invalid release manifest JSON")
	}
	names := []string{"root.json", "timestamp.json", "snapshot.json", "targets.json"}
	if _, e := os.Stat(filepath.Join(tmp, "manifest.json")); e == nil {
		names = append(names, "manifest.json")
	}
	for _, a := range arts {
		names = append(names, "targets/"+a.Name)
	}
	sort.Strings(names)
	files := make([]PublicationFile, 0, len(names))
	for _, n := range names {
		h, size, e := secure.SHA256File(filepath.Join(tmp, n))
		if e != nil {
			return nil, e
		}
		files = append(files, PublicationFile{n, h, size})
	}
	id := publicationDigest(files)
	base := filepath.Join(tufDir, "releases")
	if e = os.MkdirAll(base, 0700); e != nil {
		return nil, e
	}
	if info, e := os.Lstat(base); e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("release storage is not a real directory")
	}
	final, _ := PublicationDir(tufDir, id)
	if _, e = os.Lstat(final); e == nil {
		if e = VerifyPublication(final, id, files); e != nil {
			return nil, fmt.Errorf("existing immutable release is damaged: %w", e)
		}
	} else if !os.IsNotExist(e) {
		return nil, e
	} else {
		stage, e := os.MkdirTemp(base, ".staging-")
		if e != nil {
			return nil, e
		}
		defer os.RemoveAll(stage)
		dirs := map[string]bool{stage: true}
		for _, f := range files {
			src := filepath.Join(tmp, f.Name)
			dst := filepath.Join(stage, f.Name)
			if e = os.MkdirAll(filepath.Dir(dst), 0700); e != nil {
				return nil, e
			}
			for p := filepath.Dir(dst); p != stage && p != "."; p = filepath.Dir(p) {
				dirs[p] = true
			}
			if e = copyPublicationFile(src, dst, f); e != nil {
				return nil, e
			}
		}
		ordered := make([]string, 0, len(dirs))
		for d := range dirs {
			ordered = append(ordered, d)
		}
		sort.Slice(ordered, func(i, j int) bool { return len(ordered[i]) > len(ordered[j]) })
		for _, d := range ordered {
			if e = syncDir(d); e != nil {
				return nil, e
			}
		}
		if e = os.Rename(stage, final); e != nil {
			if v := VerifyPublication(final, id, files); v != nil {
				return nil, e
			}
		}
		if e = syncDir(base); e != nil {
			return nil, e
		}
	}
	for i := range arts {
		arts[i].Path = filepath.Join(final, "targets", arts[i].Name)
	}
	res.Artifacts = arts
	res.ID = id
	res.Digest = id
	if res.MetadataJSON == "" {
		res.MetadataJSON = "{}"
	}
	if !json.Valid([]byte(res.MetadataJSON)) {
		return nil, fmt.Errorf("invalid release manifest JSON")
	}
	return &Publication{Result: res, TrustedRoot: append([]byte(nil), trusted...), HighWater: hw, Files: files, ExpiresAt: expiry}, nil
}
func copyPublicationFile(src, dst string, f PublicationFile) error {
	in, e := os.Open(src)
	if e != nil {
		return e
	}
	defer in.Close()
	out, e := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	n, e := io.Copy(out, io.LimitReader(in, f.Length+1))
	if e == nil && n != f.Length {
		e = fmt.Errorf("staged length changed")
	}
	if e == nil {
		e = out.Sync()
	}
	closeErr := out.Close()
	if e != nil {
		return e
	}
	if closeErr != nil {
		return closeErr
	}
	h, n, e := secure.SHA256File(dst)
	if e != nil {
		return e
	}
	if n != f.Length || h != f.SHA256 {
		return fmt.Errorf("staged hash changed")
	}
	return nil
}

// VerifyPublication rejects changed bytes and links. Unreferenced staging or
// orphan directories are never served and can be inspected by an operator.
func VerifyPublication(dir, id string, files []PublicationFile) error {
	if !ValidReleaseID(id) || publicationDigest(files) != id {
		return fmt.Errorf("publication inventory digest mismatch")
	}
	info, e := os.Lstat(dir)
	if e != nil {
		return e
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("invalid publication directory")
	}
	for _, f := range files {
		if !ValidTargetName(strings.TrimPrefix(f.Name, "targets/")) || f.Length < 0 || f.Length > 200<<20 {
			return fmt.Errorf("invalid publication entry")
		}
		path := dir
		for _, part := range strings.Split(f.Name, "/") {
			path = filepath.Join(path, part)
			inf, e := os.Lstat(path)
			if e != nil {
				return e
			}
			if inf.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("publication links are not allowed")
			}
		}
		info, e := os.Stat(path)
		if e != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("publication entry is not regular")
		}
		h, n, e := secure.SHA256File(path)
		if e != nil {
			return e
		}
		if h != f.SHA256 || n != f.Length {
			return fmt.Errorf("publication content mismatch: %s", f.Name)
		}
	}
	return nil
}
