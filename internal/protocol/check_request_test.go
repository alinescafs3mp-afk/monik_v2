package protocol

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func customBase() CheckDefinition {
	return CheckDefinition{ID: "c", ServiceID: "s", URL: "http://127.0.0.1:8080", Kind: "http_health", Method: "GET", RequestVersion: 1, ExpectedStatus: []int{200}, TimeoutSeconds: 2, IntervalSeconds: 5}
}
func TestAudit4RequestContract(t *testing.T) {
	tests := []struct {
		name string
		edit func(*CheckDefinition)
	}{
		{"method", func(d *CheckDefinition) { d.Method = "DELETE" }},
		{"POST consent", func(d *CheckDefinition) { d.Method = "POST" }},
		{"GET body", func(d *CheckDefinition) { d.Body = "{}" }},
		{"header CRLF", func(d *CheckDefinition) { d.Headers = map[string]string{"X-Test": "hello\r\nEvil: x"} }},
		{"framing", func(d *CheckDefinition) { d.Headers = map[string]string{"Transfer-Encoding": "chunked"} }},
		{"duplicate case", func(d *CheckDefinition) { d.Headers = map[string]string{"Accept": "a", "accept": "b"} }},
		{"inline authorization", func(d *CheckDefinition) { d.Headers = map[string]string{"Authorization": "secret"} }},
		{"userinfo", func(d *CheckDefinition) { d.URL = "http://user:password@127.0.0.1:8080" }},
		{"secret query", func(d *CheckDefinition) { d.Path = "/health?api_key=leak" }},
		{"foreign path", func(d *CheckDefinition) { d.Path = "//example.com/health" }},
		{"DNS dial", func(d *CheckDefinition) { d.DialTarget = "example.com:80" }},
		{"wrong interval", func(d *CheckDefinition) { d.IntervalSeconds = 6 }},
		{"timeout interval", func(d *CheckDefinition) { d.TimeoutSeconds = 5 }},
		{"huge timeout", func(d *CheckDefinition) { d.TimeoutSeconds = 31; d.IntervalSeconds = 60 }},
		{"HEAD body expectation", func(d *CheckDefinition) { d.Method = "HEAD"; d.ExpectHealth = true }},
		{"inline JSON secret", func(d *CheckDefinition) {
			d.Method = "POST"
			d.AllowPOST = true
			d.Body = `{"nested":{"password":"leak"}}`
		}},
		{"lowercase form secret", func(d *CheckDefinition) {
			d.Method = "POST"
			d.AllowPOST = true
			d.Headers = map[string]string{"content-type": "application/x-www-form-urlencoded"}
			d.Body = "token=leak"
		}},
		{"body limit", func(d *CheckDefinition) { d.Method = "POST"; d.AllowPOST = true; d.Body = strings.Repeat("x", 16385) }},
		{"secret collision", func(d *CheckDefinition) {
			d.Method = "POST"
			d.AllowPOST = true
			d.Body = "{}"
			d.BodySecretID = "private"
		}},
		{"unsupported version", func(d *CheckDefinition) { d.RequestVersion = 9 }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := customBase()
			tc.edit(&d)
			if err := ValidateCheck(d); err == nil {
				t.Fatal("invalid request accepted")
			}
		})
	}
	d := customBase()
	d.Method = "POST"
	d.AllowPOST = true
	d.Body = `{"jsonrpc":"2.0","method":"health","params":[],"id":1}`
	d.Path = "/rpc?format=json"
	d.Headers = map[string]string{"Content-Type": "application/json"}
	d.IntervalSeconds = 30
	d.TimeoutSeconds = 10
	if err := ValidateCheck(d); err != nil {
		t.Fatal(err)
	}
}
func TestAudit4StrictDefinitionAndCompatibility(t *testing.T) {
	d := customBase()
	b, _ := json.Marshal(d)
	if _, e := DecodeCheck(append(b, []byte(` {}`)...)); e == nil {
		t.Fatal("trailing JSON")
	}
	if _, e := DecodeCheck([]byte(`{"url":"http://127.0.0.1","unexpected":true}`)); e == nil {
		t.Fatal("unknown fields")
	}
	legacy := customBase()
	legacy.RequestVersion = 0
	b, _ = json.Marshal(legacy)
	if strings.Contains(string(b), "request_version") {
		t.Fatal("new zero fields changed old hashes")
	}
	if RequiresCustomRequest(legacy) {
		t.Fatal("old check unnecessarily gated")
	}
	if CheckFreshness(30) != 90*time.Second {
		t.Fatal("freshness ignores interval")
	}
}
