package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestV15ProfileRejectsAmbiguousOrMultipleDocuments(t *testing.T) {
	for _, body := range []string{`{"controller_url":"https://a","controller_url":"https://b"}`, `{"auto_discover":true} {"auto_discover":false}`, "auto_discover: true\n---\nauto_discover: false\n", "controller_url: https://a\nunknown_permission: true\n", "null", "", "{broken-json", "controller_url: https://a\ncontroller_url: https://b\n"} {
		t.Run(body, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "profile")
			if e := os.WriteFile(p, []byte(body), 0600); e != nil {
				t.Fatal(e)
			}
			if _, e := LoadProfile(p); e == nil {
				t.Fatal("ambiguous/nonmapping profile accepted")
			}
		})
	}
}
func TestV15ProfileRetainsGeneratedSchemaAndEnforcesLimit(t *testing.T) {
	for _, body := range []string{`{"schema_version":3,"controller_url":"https://a","enrollment_code":"example"}`, "schema_version: 3\ncontroller_url: https://a\nenrollment_code: example\n"} {
		p := filepath.Join(t.TempDir(), "p")
		os.WriteFile(p, []byte(body), 0600)
		r, e := LoadProfile(p)
		if e != nil || r.SchemaVersion != 3 || r.EnrollmentCode != "example" {
			t.Fatal(r, e)
		}
	}
	p := filepath.Join(t.TempDir(), "large")
	os.WriteFile(p, []byte("controller_url: "+strings.Repeat("x", 128<<10)), 0600)
	if _, e := LoadProfile(p); e == nil {
		t.Fatal("oversized profile accepted")
	}
}
