package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"os"
	"strings"
)

//go:embed all:dist
var embedded embed.FS

func Serve(w http.ResponseWriter, r *http.Request) {
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
		p = "index.html"
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
