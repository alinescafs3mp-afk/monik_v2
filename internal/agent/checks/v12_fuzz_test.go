package checks

import (
	"encoding/json"
	"testing"
)

// Untrusted UI definitions must reject malformed types without panicking or
// producing scheduling values outside the supported contract. No network I/O.
func FuzzV12TrialDefinition(f *testing.F) {
	for _, s := range []string{
		`{}`, `null`, `{"url":"http://127.0.0.1:8080/health","method":"GET"}`,
		`{"url":"http://[::1]:80/","timeout_seconds":1e100}`,
		`{"url":"http://127.0.0.1/","headers":{"X-Test":["value"]}}`,
		`{"url":"http://127.0.0.1/","method":"POST","allow_post":true,"body":"{}"}`,
		`{"url":"http://127.0.0.1/","expected_status":[200,null,"500"]}`,
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > 64<<10 {
			return
		}
		var input map[string]any
		if json.Unmarshal(raw, &input) != nil {
			return
		}
		d, err := ParseTrial(input)
		if err != nil {
			return
		}
		if d.TimeoutSeconds < 1 || d.TimeoutSeconds > 30 || d.IntervalSeconds < 5 || d.IntervalSeconds > 3600 || d.TimeoutSeconds >= d.IntervalSeconds {
			t.Fatalf("invalid accepted schedule: %+v", d)
		}
		if d.Method != "GET" && d.Method != "HEAD" && d.Method != "OPTIONS" && d.Method != "POST" {
			t.Fatalf("unsupported method accepted: %q", d.Method)
		}
	})
}
