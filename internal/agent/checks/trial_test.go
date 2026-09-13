package checks

import "testing"

func TestParseTrialRejectsSecretPlaintextAndMetadata(t *testing.T) {
	if _, err := ParseTrial(map[string]any{"url": "http://127.0.0.1/", "value": "secret"}); err == nil {
		t.Fatal("plaintext accepted")
	}
	if _, err := ParseTrial(map[string]any{"url": "http://169.254.169.254/", "method": "GET"}); err == nil {
		t.Fatal("metadata accepted")
	}
	if _, err := ParseTrial(map[string]any{"url": "http://127.0.0.1/", "method": "POST"}); err == nil {
		t.Fatal("POST accepted")
	}
	def, err := ParseTrial(map[string]any{"url": "http://127.0.0.1:9/health", "method": "GET", "expected_status": []any{float64(200)}})
	if err != nil || def.Method != "GET" || len(def.ExpectedStatus) != 1 {
		t.Fatalf("%+v %v", def, err)
	}
}
