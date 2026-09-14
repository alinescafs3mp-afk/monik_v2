package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"os"
	"path"
	"strings"
)

//go:embed all:dist
var embedded embed.FS

func Serve(w http.ResponseWriter, r *http.Request) {
	if !DistExists() {
		http.Error(w, "UI is not built. Run make ui before building monik-server (or use make all).", http.StatusServiceUnavailable)
		return
	}
	sub, err := fs.Sub(embedded, "dist")
	if err != nil {
		http.Error(w, "ui not embedded", 500)
		return
	}
	p := strings.TrimPrefix(r.URL.Path, "/")
	if p == "" {
		p = "index.html"
	}
	if _, err := fs.Stat(sub, p); err != nil {
		// A stale dynamic-import URL is not an SPA route. Returning HTML with 200
		// hides deployment/cache failures behind a misleading module parse error.
		if strings.HasPrefix(p, "assets/") || path.Ext(p) != "" {
			w.Header().Set("Cache-Control", "no-store")
			http.NotFound(w, r)
			return
		}
		p = "index.html"
	}
	if p == "index.html" {
		w.Header().Set("Cache-Control", "no-cache")
	} else if strings.HasPrefix(p, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	http.ServeFileFS(w, r, sub, p)
}

func DistExists() bool {
	_, err := embedded.ReadFile("dist/index.html")
	return err == nil
}

func WriteDevIndex(dir string) error {
	return os.MkdirAll(dir, 0o755)
}
