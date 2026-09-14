package checks

import (
	"context"
	"encoding/json"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func advanced(url string) protocol.CheckDefinition {
	return protocol.CheckDefinition{ID: "check", ServiceID: "service", RequestVersion: 1, URL: url, Method: "GET", Kind: "http_health", ExpectedStatus: []int{200}, TimeoutSeconds: 2, IntervalSeconds: 5}
}
func TestAudit4CustomPOSTRoundTripAndNoSecretTelemetry(t *testing.T) {
	var calls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		b, _ := io.ReadAll(r.Body)
		if r.Method != "POST" || r.URL.RequestURI() != "/rpc?format=json" || r.Header.Get("X-App") != "monik" || r.Header.Get("Authorization") != "Bearer never-log-me" || !strings.Contains(string(b), "private-body-token") {
			t.Errorf("request template not applied")
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"checks":[{"ready":true}]}`)
	}))
	defer ts.Close()
	d := advanced(ts.URL)
	d.Method = "POST"
	d.AllowPOST = true
	d.Path = "/rpc?format=json"
	d.Headers = map[string]string{"Content-Type": "application/json", "X-App": "monik"}
	d.SecretID = "auth"
	d.SecretHeader = "Authorization"
	d.BodySecretID = "body"
	d.ExpectJSONPath = "/checks/0/ready"
	d.ExpectJSONType = "boolean"
	d.ExpectJSONValue = "true"
	o := RunRequest(context.Background(), d, nil, "Authorization", "Bearer never-log-me", `{"token":"private-body-token"}`)
	if o.AppResult != "pass" || calls.Load() != 1 {
		t.Fatalf("%+v", o)
	}
	b, _ := json.Marshal(o)
	for _, s := range []string{"never-log-me", "private-body-token", "format=json"} {
		if strings.Contains(string(b), s) {
			t.Fatal("private request material in observation")
		}
	}
}
func TestAudit4POSTIsNeverGuessedOrRedirected(t *testing.T) {
	var calls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Location", "http://169.254.169.254/latest")
		w.WriteHeader(302)
	}))
	defer ts.Close()
	d := advanced(ts.URL)
	d.Method = "POST"
	o := Run(context.Background(), d, nil, "", "")
	if o.Transport != "blocked" || calls.Load() != 0 {
		t.Fatal("POST without consent dialed")
	}
	d.AllowPOST = true
	o = Run(context.Background(), d, nil, "", "")
	if calls.Load() != 1 || o.HTTPStatus == nil || *o.HTTPStatus != 302 || o.AppResult != "fail" {
		t.Fatalf("redirect followed or false success: %+v", o)
	}
}
func TestAudit4TypedJSONTruthAndCompleteBody(t *testing.T) {
	for _, body := range []string{`{"ready":"true"}`, `{"ready":true} garbage`, `{"ready":true} {"other":false}`} {
		t.Run(body, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, body)
			}))
			defer ts.Close()
			d := advanced(ts.URL)
			d.ExpectJSONPath = "ready"
			d.ExpectJSONType = "boolean"
			d.ExpectJSONValue = "true"
			if o := Run(context.Background(), d, nil, "", ""); o.AppResult != "fail" {
				t.Fatalf("accepted invalid typed response: %+v", o)
			}
		})
	}
}
func TestAudit4RefusedAndTLSHaveDifferentEvidence(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	addr := ts.URL
	ts.Close()
	d := advanced(addr)
	o := Run(context.Background(), d, nil, "", "")
	if o.Transport != "refused" || o.FailureLayer != "connection" {
		t.Fatalf("%+v", o)
	}
	tls := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("invalid TLS reached app") }))
	defer tls.Close()
	d.URL = tls.URL
	o = Run(context.Background(), d, nil, "", "")
	if o.Transport != "tls_error" {
		t.Fatalf("%+v", o)
	}
}
func TestAudit4AssertionsBoundHugeNumberAndKeepNegativeHealth(t *testing.T) {
	var v any
	dec := json.NewDecoder(strings.NewReader(`{"v":1e999999999}`))
	dec.UseNumber()
	if dec.Decode(&v) != nil {
		t.Fatal("fixture")
	}
	start := time.Now()
	if matchJSON(v, "v", "1", "number") {
		t.Fatal("wrong numeric match")
	}
	if time.Since(start) > time.Second {
		t.Fatal("unbounded exponent")
	}
	token, _ := healthToken([]byte(`{"status":"ok","ready":false}`), "application/json")
	if PositiveHealth(token) {
		t.Fatal("positive masks negative")
	}
}
func TestAudit4TrialPreservesWholeRequest(t *testing.T) {
	d := advanced("http://127.0.0.1:8888")
	d.Method = "POST"
	d.AllowPOST = true
	d.Body = `{"method":"health"}`
	d.Path = "/rpc?mode=brief"
	d.Headers = map[string]string{"X-App": "x"}
	d.IntervalSeconds = 30
	d.TimeoutSeconds = 10
	b, _ := json.Marshal(d)
	var p map[string]any
	json.Unmarshal(b, &p)
	trial, e := ParseTrial(p)
	if e != nil || trial.Body != d.Body || trial.IntervalSeconds != 30 || trial.Path != d.Path || trial.Headers["X-App"] != "x" {
		t.Fatalf("trial silently changed request: %+v %v", trial, e)
	}
}

func TestAudit4TrialOfPausedCheckDoesNotAlterStoredDefinition(t *testing.T) {
	params := map[string]any{"url": "http://127.0.0.1:8000", "paused": true, "ignored": true}
	d, err := ParseTrial(params)
	if err != nil || d.Paused || d.Ignored {
		t.Fatalf("explicit diagnostic was paused: %+v %v", d, err)
	}
	if params["paused"] != true || params["ignored"] != true {
		t.Fatal("trial changed scheduled template")
	}
}

func TestAudit4MalformedURLCannotLeakQueryInFailure(t *testing.T) {
	d := protocol.CheckDefinition{URL: "http://127.0.0.1/%zz?private=do-not-echo-123", Kind: "baseline_http", Method: "GET"}
	obs := Run(context.Background(), d, nil, "", "")
	b, err := json.Marshal(obs)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "do-not-echo-123") {
		t.Fatal("URL parse error leaked query")
	}
}
