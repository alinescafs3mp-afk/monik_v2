package checks

import (
	"context"
	"encoding/json"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReviewFeedbackIsBoundedAndDoesNotCopySecrets(t *testing.T) {
	for _, tc := range []struct{ name, ct, body, health, source, state string }{{"json", "application/json", `{"status":"UP","token":"DO_NOT_COPY","error":"private trace"}`, "up", "status", "sampled"}, {"rfc", "application/health+json", `{"status":"pass"}`, "pass", "status", "sampled"}, {"contradiction", "application/json", `{"status":"ok","ready":false}`, "false", "ready", "sampled"}, {"html", "text/html", `<h1>DO_NOT_COPY</h1>`, "", "", "sampled"}, {"plain", "text/plain", "OK", "ok", "text", "sampled"}, {"empty", "text/plain", "", "", "", "empty"}, {"large", "application/json", strings.Repeat("x", 70000), "", "", "limited"}} {
		t.Run(tc.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tc.ct)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer ts.Close()
			o := Run(context.Background(), protocol.CheckDefinition{URL: ts.URL, Kind: "baseline_http", Method: "GET", TimeoutSeconds: 2}, []net.IP{net.ParseIP("127.0.0.1")}, "", "")
			if o.Feedback == nil || o.Feedback.Health != tc.health || o.Feedback.HealthSource != tc.source || o.Feedback.BodyState != tc.state {
				t.Fatalf("%+v", o.Feedback)
			}
			b, _ := json.Marshal(o)
			if strings.Contains(string(b), "DO_NOT_COPY") || strings.Contains(string(b), "private trace") {
				t.Fatal("body secret copied")
			}
			if o.AppResult != "not_configured" {
				t.Fatal("self-reported health must not invent configured application health")
			}
		})
	}
}
func TestReviewHeadFallbackAndHealthPath(t *testing.T) {
	var got []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Method+" "+r.URL.Path)
		if r.Method == "HEAD" {
			w.WriteHeader(405)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer ts.Close()
	o := Run(context.Background(), protocol.CheckDefinition{URL: ts.URL + "/", Path: "/healthz", Kind: "http_health", Method: "HEAD", ExpectedStatus: []int{200}, TimeoutSeconds: 2}, []net.IP{net.ParseIP("127.0.0.1")}, "", "")
	if strings.Join(got, ",") != "HEAD /healthz,GET /healthz" || o.AppResult != "pass" || o.Feedback.Method != "GET" {
		t.Fatalf("%v %+v", got, o)
	}
}
func TestReviewTrialRejectsSilentlyTruncatedFields(t *testing.T) {
	for _, p := range []map[string]any{{"url": "http://127.0.0.1/", "expected_status": []any{200.5}}, {"url": "http://127.0.0.1/", "kind": "anything"}, {"url": "http://127.0.0.1/", "method": "HEAD", "expect_text": "ok"}, {"url": "http://127.0.0.1/", "path": "not-absolute"}, {"url": "http://127.0.0.1/", "timeout_seconds": 0.5}} {
		if _, err := ParseTrial(p); err == nil {
			t.Fatalf("accepted %+v", p)
		}
	}
}
