package webui

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestV12MissingAssetIsNotSuccessfulHTML(t *testing.T) {
	if !DistExists() {
		t.Skip("UI must be built for asset serving acceptance")
	}
	w := httptest.NewRecorder()
	Serve(w, httptest.NewRequest("GET", "/assets/previous-build.js", nil))
	if w.Code != 404 || strings.Contains(w.Body.String(), "<html") {
		t.Fatalf("missing JS returned SPA as HTTP %d", w.Code)
	}
	w = httptest.NewRecorder()
	Serve(w, httptest.NewRequest("GET", "/machines/existing", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "<html") {
		t.Fatalf("SPA deep link unavailable: %d", w.Code)
	}
	if !strings.Contains(w.Header().Get("Cache-Control"), "no-cache") {
		t.Fatal("app shell can become stale across deployment")
	}
}
