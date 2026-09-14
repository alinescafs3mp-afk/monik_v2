package server

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"github.com/alinescafs3mp-afk/monik_v2/internal/storage"
	"github.com/alinescafs3mp-afk/monik_v2/internal/tufutil"
)

// Committed database trust is authoritative. Legacy files are only read when
// no new publication has been committed yet; they are never overwritten.
func (a *App) releaseTrust() (storage.ReleaseTrust, error) {
	v, e := a.Store.ReleaseTrust()
	if !errors.Is(e, storage.ErrNotFound) {
		return v, e
	}
	dir := filepath.Join(a.Cfg.DataDir, "tuf")
	root, e := tufutil.LoadTrustedRoot(dir)
	if os.IsNotExist(e) {
		if _, ve := os.Lstat(tufutil.HighWaterPath(dir)); !os.IsNotExist(ve) {
			return v, fmt.Errorf("update root is missing while legacy version state exists")
		}
		var count int
		if ce := a.Store.DB.QueryRow("SELECT COUNT(*) FROM releases WHERE trust_ok=1").Scan(&count); ce != nil {
			return v, ce
		}
		if count > 0 {
			return v, fmt.Errorf("update root is missing while trusted catalogue entries exist")
		}
		return storage.ReleaseTrust{}, nil
	}
	if e != nil {
		return v, e
	}
	hw, e := tufutil.ReadHighWater(dir)
	return storage.ReleaseTrust{Root: root, Versions: hw}, e
}
func (a *App) updateRoot() ([]byte, error) {
	v, e := a.releaseTrust()
	if e != nil {
		return nil, e
	}
	if len(v.Root) == 0 {
		return nil, os.ErrNotExist
	}
	return v.Root, nil
}
func (a *App) importImmutableRelease(op *protocol.Operation, req protocol.SubmitOperation) error {
	path, _ := req.Params["bundle_path"].(string)
	enroll, _ := req.Params["enroll_root"].(bool)
	if path == "" {
		return fmt.Errorf("bundle_path required")
	}
	state, e := a.releaseTrust()
	if e != nil {
		return e
	}
	p, e := tufutil.PreparePublication(path, filepath.Join(a.Cfg.DataDir, "tuf"), tufutil.ImportOpts{TrustedRoot: state.Root, Enroll: enroll, Now: a.Clock.Now().UTC()}, state.Versions)
	if e != nil {
		return e
	}
	return a.Store.CommitPublication(p, state.Revision, op.ID)
}
func (a *App) handlePublishedMetadata(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	switch name {
	case "root.json", "timestamp.json", "snapshot.json", "targets.json":
	default:
		a.writeErr(w, 404, "not_found", "metadata not found")
		return
	}
	a.servePublication(w, r, name)
}
func (a *App) handlePublishedArtifact(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !tufutil.ValidTargetName(name) {
		a.writeErr(w, 400, "bad_name", "invalid target name")
		return
	}
	a.servePublication(w, r, "targets/"+name)
}
func (a *App) servePublication(w http.ResponseWriter, r *http.Request, name string) {
	if _, _, ok := a.agentFrom(r); !ok {
		a.writeErr(w, 401, "unauthenticated", "agent credential required")
		return
	}
	id := r.PathValue("release")
	if !tufutil.ValidReleaseID(id) {
		a.writeErr(w, 404, "not_found", "release not found")
		return
	}
	digest, files, e := a.Store.Publication(id)
	if errors.Is(e, storage.ErrNotFound) {
		a.writeErr(w, 404, "not_found", "release not found")
		return
	}
	if e != nil {
		a.writeErr(w, 500, "catalogue", "release catalogue unreadable")
		return
	}
	var expected *tufutil.PublicationFile
	for i := range files {
		if files[i].Name == name {
			expected = &files[i]
			break
		}
	}
	if expected == nil {
		a.writeErr(w, 404, "not_found", "file not in release")
		return
	}
	dir, e := tufutil.PublicationDir(filepath.Join(a.Cfg.DataDir, "tuf"), digest)
	if e != nil {
		a.writeErr(w, 500, "catalogue", "invalid publication")
		return
	}
	if info, e := os.Lstat(dir); e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		a.writeErr(w, 503, "release_damaged", "release directory unavailable")
		return
	}
	// Go Root confines all resolution, including links in intermediate directories.
	root, e := os.OpenRoot(dir)
	if e != nil {
		a.writeErr(w, 503, "release_unavailable", "release files unavailable")
		return
	}
	defer root.Close()
	f, e := root.Open(name)
	if e != nil {
		a.writeErr(w, 503, "release_unavailable", "release file unavailable")
		return
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() || st.Size() != expected.Length {
		a.writeErr(w, 503, "release_damaged", "release file is damaged")
		return
	}
	if strings.HasSuffix(name, ".json") {
		w.Header().Set("Content-Type", "application/json")
	} else {
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	w.Header().Set("ETag", `"`+expected.SHA256+`"`)
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, filepath.Base(name), st.ModTime(), f)
}
