package checks

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
)

func TestAuditHTTPBaselineAndApplicationAreDifferent(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) }))
	defer ts.Close()
	d := protocol.CheckDefinition{ID: "c", ServiceID: "s", URL: ts.URL, Kind: "baseline_http", TimeoutSeconds: 2}
	got := Run(context.Background(), d, nil, "", "")
	if got.Transport != "ok" || got.HTTPStatus == nil || *got.HTTPStatus != 401 || got.AppResult != "not_configured" {
		t.Fatalf("%+v", got)
	}
	d.Kind = "http_health"
	d.ExpectedStatus = []int{200}
	got = Run(context.Background(), d, nil, "", "")
	if got.Transport != "ok" || got.AppResult != "fail" {
		t.Fatalf("%+v", got)
	}
}
func TestAuditBodyExpectationsUseGET(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			w.WriteHeader(405)
			return
		}
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer ts.Close()
	d := protocol.CheckDefinition{ID: "c", ServiceID: "s", URL: ts.URL, Kind: "http_health", ExpectedStatus: []int{200}, ExpectJSONPath: "status", ExpectJSONValue: "ok", TimeoutSeconds: 2}
	got := Run(context.Background(), d, nil, "", "")
	if got.AppResult != "pass" {
		t.Fatalf("%+v", got)
	}
}
func TestAuditProbeRejectsMutationAndExternalRedirect(t *testing.T) {
	var calls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Redirect(w, r, "http://169.254.169.254/", 302)
	}))
	defer ts.Close()
	d := protocol.CheckDefinition{ID: "c", ServiceID: "s", URL: ts.URL, Kind: "baseline_http", Method: "POST", TimeoutSeconds: 2}
	got := Run(context.Background(), d, nil, "", "")
	if got.Transport != "blocked" || calls.Load() != 0 {
		t.Fatal("mutating method reached service")
	}
	d.Method = "GET"
	got = Run(context.Background(), d, nil, "", "")
	if got.HTTPStatus == nil || *got.HTTPStatus != 302 || calls.Load() != 1 {
		t.Fatalf("redirect followed %+v", got)
	}
}
func TestAuditOversizedAndBrokenBodiesAreNotSuccessful(t *testing.T) {
	for _, broken := range []bool{false, true} {
		t.Run(map[bool]string{false: "oversized", true: "truncated"}[broken], func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if broken {
					w.Header().Set("Content-Length", "200")
					w.Write([]byte("ok"))
				} else {
					w.Write([]byte(strings.Repeat("x", 65537)))
				}
			}))
			defer ts.Close()
			d := protocol.CheckDefinition{ID: "c", ServiceID: "s", URL: ts.URL, Method: "GET", Kind: "http_health", ExpectedStatus: []int{200}, TimeoutSeconds: 2}
			got := Run(context.Background(), d, nil, "", "")
			if got.Quality != protocol.QualityError || got.AppResult != "fail" {
				t.Fatalf("false success %+v", got)
			}
		})
	}
}
func TestAuditIPv6WithoutPortIsAValidDialTarget(t *testing.T) {
	got := Run(context.Background(), protocol.CheckDefinition{ID: "c", ServiceID: "s", URL: "http://[::1]", Method: "POST"}, nil, "", "")
	if got.DialTarget != "[::1]:80" || !strings.Contains(got.AppReason, "POST requires explicit") {
		t.Fatalf("%+v", got)
	}
}
