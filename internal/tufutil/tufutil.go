package tufutil

import (
	"archive/tar"
	"compress/gzip"
	"crypto"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/secure"
	"github.com/sigstore/sigstore/pkg/signature"
	"github.com/theupdateframework/go-tuf/v2/metadata"
)

type KeySet struct {
	Dir string
	Root, Targets, Snapshot, Timestamp ed25519.PrivateKey
}

type BundleResult struct {
	ID            string
	Version       string
	Digest        string
	Notes         string
	MetadataJSON  string
	Platforms     []map[string]string
	Artifacts     []Artifact
}

type Artifact struct {
	OS, Arch, Name, SHA256, Path string
	Length                       int64
}

func InitKeys(dir string) (*KeySet, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	ks := &KeySet{Dir: dir}
	var err error
	if ks.Root, err = loadOrGen(filepath.Join(dir, "root.ed25519")); err != nil {
		return nil, err
	}
	if ks.Targets, err = loadOrGen(filepath.Join(dir, "targets.ed25519")); err != nil {
		return nil, err
	}
	if ks.Snapshot, err = loadOrGen(filepath.Join(dir, "snapshot.ed25519")); err != nil {
		return nil, err
	}
	if ks.Timestamp, err = loadOrGen(filepath.Join(dir, "timestamp.ed25519")); err != nil {
		return nil, err
	}
	return ks, nil
}

func loadOrGen(path string) (ed25519.PrivateKey, error) {
	if b, err := os.ReadFile(path); err == nil && len(b) == ed25519.PrivateKeySize {
		return ed25519.PrivateKey(b), nil
	}
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, priv, 0o600); err != nil {
		return nil, err
	}
	return priv, nil
}

func signerOf(k ed25519.PrivateKey) (signature.Signer, error) {
	return signature.LoadSigner(k, crypto.Hash(0))
}

func SignRepository(keys *KeySet, repoDir string, artifacts map[string]string, targetsDays, tsDays int) error {
	if err := os.MkdirAll(filepath.Join(repoDir, "targets"), 0o755); err != nil {
		return err
	}
	targets := metadata.Targets(time.Now().AddDate(0, 0, targetsDays).UTC())
	for targetPath, local := range artifacts {
		info, err := metadata.TargetFile().FromFile(local, "sha256")
		if err != nil {
			return err
		}
		targets.Signed.Targets[targetPath] = info
		dst := filepath.Join(repoDir, "targets", targetPath)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		b, err := os.ReadFile(local)
		if err != nil {
			return err
		}
		if err := os.WriteFile(dst, b, 0o644); err != nil {
			return err
		}
	}
	root := metadata.Root(time.Now().AddDate(1, 0, 0).UTC())
	for name, k := range map[string]ed25519.PrivateKey{"root": keys.Root, "targets": keys.Targets, "snapshot": keys.Snapshot, "timestamp": keys.Timestamp} {
		key, err := metadata.KeyFromPublicKey(k.Public())
		if err != nil {
			return err
		}
		if err := root.Signed.AddKey(key, name); err != nil {
			return err
		}
	}
	snapshot := metadata.Snapshot(time.Now().AddDate(0, 0, targetsDays).UTC())
	timestamp := metadata.Timestamp(time.Now().AddDate(0, 0, tsDays).UTC())

	tsSigner, err := signerOf(keys.Targets)
	if err != nil {
		return err
	}
	if _, err := targets.Sign(tsSigner); err != nil {
		return err
	}
	if err := targets.ToFile(filepath.Join(repoDir, "targets.json"), true); err != nil {
		return err
	}
	snapshot.Signed.Meta["targets.json"] = metadata.MetaFile(targets.Signed.Version)
	ssSigner, err := signerOf(keys.Snapshot)
	if err != nil {
		return err
	}
	if _, err := snapshot.Sign(ssSigner); err != nil {
		return err
	}
	if err := snapshot.ToFile(filepath.Join(repoDir, "snapshot.json"), true); err != nil {
		return err
	}
	timestamp.Signed.Meta["snapshot.json"] = metadata.MetaFile(snapshot.Signed.Version)
	tmSigner, err := signerOf(keys.Timestamp)
	if err != nil {
		return err
	}
	if _, err := timestamp.Sign(tmSigner); err != nil {
		return err
	}
	if err := timestamp.ToFile(filepath.Join(repoDir, "timestamp.json"), true); err != nil {
		return err
	}
	rtSigner, err := signerOf(keys.Root)
	if err != nil {
		return err
	}
	if _, err := root.Sign(rtSigner); err != nil {
		return err
	}
	if err := root.ToFile(filepath.Join(repoDir, "root.json"), true); err != nil {
		return err
	}
	if err := root.ToFile(filepath.Join(repoDir, "1.root.json"), true); err != nil {
		return err
	}
	if err := root.VerifyDelegate("root", root); err != nil {
		return fmt.Errorf("root verify: %w", err)
	}
	if err := root.VerifyDelegate("targets", targets); err != nil {
		return fmt.Errorf("targets verify: %w", err)
	}
	if err := root.VerifyDelegate("snapshot", snapshot); err != nil {
		return fmt.Errorf("snapshot verify: %w", err)
	}
	if err := root.VerifyDelegate("timestamp", timestamp); err != nil {
		return fmt.Errorf("timestamp verify: %w", err)
	}
	return nil
}

func VerifyLocal(repoDir, targetName string) (*metadata.TargetFiles, []byte, error) {
	root := metadata.Root()
	if _, err := root.FromFile(filepath.Join(repoDir, "root.json")); err != nil {
		return nil, nil, err
	}
	ts := metadata.Timestamp()
	if _, err := ts.FromFile(filepath.Join(repoDir, "timestamp.json")); err != nil {
		return nil, nil, err
	}
	if err := root.VerifyDelegate("timestamp", ts); err != nil {
		return nil, nil, err
	}
	sn := metadata.Snapshot()
	if _, err := sn.FromFile(filepath.Join(repoDir, "snapshot.json")); err != nil {
		return nil, nil, err
	}
	if err := root.VerifyDelegate("snapshot", sn); err != nil {
		return nil, nil, err
	}
	tg := metadata.Targets()
	if _, err := tg.FromFile(filepath.Join(repoDir, "targets.json")); err != nil {
		return nil, nil, err
	}
	if err := root.VerifyDelegate("targets", tg); err != nil {
		return nil, nil, err
	}
	info, ok := tg.Signed.Targets[targetName]
	if !ok {
		return nil, nil, fmt.Errorf("target %s not in metadata", targetName)
	}
	b, err := os.ReadFile(filepath.Join(repoDir, "targets", targetName))
	if err != nil {
		return nil, nil, err
	}
	if err := info.VerifyLengthHashes(b); err != nil {
		return nil, nil, err
	}
	return info, b, nil
}

func PackBundle(repoDir, outPath, version, notes string) error {
	f, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()
	manifest := map[string]any{
		"schema_version": 3, "worker_version": version, "service_host_version": version,
		"notes": notes, "not_unsigned": true,
	}
	mb, _ := json.MarshalIndent(manifest, "", "  ")
	if err := addTarBytes(tw, "manifest.json", mb); err != nil {
		return err
	}
	return filepath.Walk(repoDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, err := filepath.Rel(repoDir, path)
		if err != nil {
			return err
		}
		if strings.Contains(rel, "..") {
			return fmt.Errorf("bad path")
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return addTarBytes(tw, rel, b)
	})
}

func addTarBytes(tw *tar.Writer, name string, b []byte) error {
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0644, Size: int64(len(b)), Typeflag: tar.TypeReg}); err != nil {
		return err
	}
	_, err := tw.Write(b)
	return err
}

func ImportBundle(bundlePath, destDir string) (*BundleResult, error) {
	f, err := os.Open(bundlePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	tmp, err := os.MkdirTemp("", "monik-import-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	var total int64
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if hdr.Typeflag != tar.TypeReg {
			if hdr.Typeflag == tar.TypeDir {
				continue
			}
			return nil, fmt.Errorf("unsafe archive entry %s", hdr.Name)
		}
		name := filepath.Clean(hdr.Name)
		if strings.HasPrefix(name, "..") || filepath.IsAbs(name) || strings.Contains(name, `\`) {
			return nil, fmt.Errorf("path traversal rejected")
		}
		if hdr.Size > 200<<20 {
			return nil, fmt.Errorf("entry too large")
		}
		total += hdr.Size
		if total > 512<<20 {
			return nil, fmt.Errorf("bundle too large")
		}
		dst := filepath.Join(tmp, name)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return nil, err
		}
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o644)
		if err != nil {
			return nil, err
		}
		if _, err := io.CopyN(out, tr, hdr.Size); err != nil {
			out.Close()
			return nil, err
		}
		out.Close()
	}
	rootPath := filepath.Join(tmp, "root.json")
	if _, err := os.Stat(rootPath); err != nil {
		if _, err2 := os.Stat(filepath.Join(tmp, "repository", "root.json")); err2 == nil {
			tmp = filepath.Join(tmp, "repository")
		} else {
			return nil, fmt.Errorf("bundle missing TUF root.json")
		}
	}
	root := metadata.Root()
	if _, err := root.FromFile(filepath.Join(tmp, "root.json")); err != nil {
		return nil, err
	}
	tg := metadata.Targets()
	if _, err := tg.FromFile(filepath.Join(tmp, "targets.json")); err != nil {
		return nil, err
	}
	if err := root.VerifyDelegate("targets", tg); err != nil {
		return nil, fmt.Errorf("targets signature: %w", err)
	}
	ts := metadata.Timestamp()
	if _, err := ts.FromFile(filepath.Join(tmp, "timestamp.json")); err != nil {
		return nil, err
	}
	if err := root.VerifyDelegate("timestamp", ts); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return nil, err
	}
	if err := copyTree(tmp, destDir); err != nil {
		return nil, err
	}
	res := &BundleResult{ID: secure.SHA256Bytes([]byte(time.Now().String()))[:16]}
	var plats []map[string]string
	var arts []Artifact
	for name, info := range tg.Signed.Targets {
		p := filepath.Join(destDir, "targets", name)
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		if err := info.VerifyLengthHashes(b); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		sum := secure.SHA256Bytes(b)
		osn, arch := parsePlatform(name)
		arts = append(arts, Artifact{OS: osn, Arch: arch, Name: name, SHA256: sum, Path: p, Length: int64(len(b))})
		plats = append(plats, map[string]string{"os": osn, "arch": arch, "name": name})
	}
	res.Artifacts = arts
	res.Platforms = plats
	res.Digest = secure.SHA256Bytes([]byte(fmt.Sprintf("%v", tg.Signed.Targets)))
	man := filepath.Join(tmp, "manifest.json")
	if b, err := os.ReadFile(man); err == nil {
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		if v, ok := m["worker_version"].(string); ok {
			res.Version = v
		}
		if v, ok := m["notes"].(string); ok {
			res.Notes = v
		}
		res.MetadataJSON = string(b)
	}
	if res.Version == "" {
		res.Version = "unknown"
	}
	return res, nil
}

func parsePlatform(name string) (osn, arch string) {
	n := strings.ToLower(name)
	switch {
	case strings.Contains(n, "linux"):
		osn = "linux"
	case strings.Contains(n, "windows"):
		osn = "windows"
	case strings.Contains(n, "darwin"):
		osn = "darwin"
	default:
		osn = "any"
	}
	switch {
	case strings.Contains(n, "amd64") || strings.Contains(n, "x86_64"):
		arch = "amd64"
	case strings.Contains(n, "arm64") || strings.Contains(n, "aarch64"):
		arch = "arm64"
	default:
		arch = "amd64"
	}
	return
}

func copyTree(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, b, info.Mode())
	})
}

func TrustedRootBytes(repoDir string) ([]byte, error) {
	return os.ReadFile(filepath.Join(repoDir, "root.json"))
}
