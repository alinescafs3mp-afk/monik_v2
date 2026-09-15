package server

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"github.com/alinescafs3mp-afk/monik_v2/internal/secure"
)

func TestV14MissingSelectedOrWatchedServiceNeverHidesFailure(t *testing.T) {
	a, _ := testApp(t)
	v14Register(t, a, "host")
	now := a.Clock.Now()
	ep := protocol.DiscoveredEndpoint{ServiceID: "gone", URL: "http://127.0.0.1:9310", DialTarget: "127.0.0.1:9310", Source: "listener+http", SpeaksHTTP: true}
	if e := a.Store.UpsertService(ep, "host"); e != nil {
		t.Fatal(e)
	}
	_, e := a.Store.DB.Exec(`UPDATE service_presence SET state='missing',missing_since=?,absent_snapshots=2 WHERE service_id='gone'`, now.Format(time.RFC3339Nano))
	if e != nil {
		t.Fatal(e)
	}
	cfg := protocol.DefaultAgentConfig()
	cfg.Checks = []protocol.CheckDefinition{{ID: "check", ServiceID: "gone", URL: ep.URL, DialTarget: ep.DialTarget, Kind: "baseline_http", Method: "GET", TimeoutSeconds: 2, IntervalSeconds: 5}}
	setConfig := func(rev int64, applied bool) {
		t.Helper()
		b, _ := json.Marshal(cfg)
		hash := secure.SHA256Bytes(b)
		if e := a.Store.SetDesired("host", rev, hash, string(b)); e != nil {
			t.Fatal(e)
		}
		if applied {
			if e := a.Store.SetApplied("host", rev, hash); e != nil {
				t.Fatal(e)
			}
		}
	}
	state := func() serviceSummary {
		t.Helper()
		r, e := a.serviceSummaries("host", now)
		if e != nil || len(r) != 1 {
			t.Fatal(r, e)
		}
		return r[0]
	}
	setConfig(2, true)
	if state().InventoryArchived {
		t.Fatal("enabled check hidden because listener is absent")
	}
	cfg.Paused = true
	setConfig(3, true)
	if state().InventoryArchived {
		t.Fatal("machine pause must not change service retention")
	}
	cfg.Paused = false
	cfg.Checks[0].Paused = true
	setConfig(4, false)
	if state().InventoryArchived {
		t.Fatal("pause request was treated as applied")
	}
	b, _ := json.Marshal(cfg)
	if e := a.Store.SetApplied("host", 4, secure.SHA256Bytes(b)); e != nil {
		t.Fatal(e)
	}
	if !state().InventoryArchived || state().State != "inactive" {
		t.Fatal("disabled missing discovery did not retire", state())
	}
	if _, e := a.Store.DB.Exec(`UPDATE services SET pinned=1 WHERE id='gone'`); e != nil {
		t.Fatal(e)
	}
	if state().InventoryArchived {
		t.Fatal("explicit screen selection was silently removed")
	}
	if _, e := a.Store.DB.Exec(`UPDATE services SET pinned=0,source='manual' WHERE id='gone'`); e != nil {
		t.Fatal(e)
	}
	if state().InventoryArchived {
		t.Fatal("manually configured service was auto-retired")
	}
}

func TestV14ProfileExternalAddressIsIndependentAndTLSChecked(t *testing.T) {
	a, h := testApp(t)
	call := audit6HTTP(t, a, h, time.Minute)
	originalID := a.ControllerID()
	originalCA := string(a.CACertPEM())
	oldURL := a.Cfg.AdvertisedURL
	type response struct {
		Profile    string `json:"profile_url"`
		Advertised string `json:"advertised_url"`
		CA         string `json:"ca_cert_pem"`
		TLS        struct {
			Matches       bool `json:"matches"`
			RouteVerified bool `json:"route_verified"`
		} `json:"tls"`
	}
	getProfile := func(path string) response {
		t.Helper()
		w := call("GET", path, nil)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "PRIVATE KEY") {
			t.Fatal("private key in profile response")
		}
		var r response
		if e := json.Unmarshal(w.Body.Bytes(), &r); e != nil {
			t.Fatal(e)
		}
		return r
	}
	r := getProfile("/api/v1/enrollment")
	if r.Profile != "https://46.150.103.61:8777" || r.Advertised != oldURL || r.TLS.Matches || r.TLS.RouteVerified {
		t.Fatalf("invalid public profile preflight: %+v", r)
	}
	r = getProfile("/api/v1/enrollment?controller_url=" + url.QueryEscape(oldURL+"/"))
	if r.Profile != oldURL || !r.TLS.Matches || r.CA != originalCA {
		t.Fatal(r)
	}
	if _, e := a.TLS.AddServerName(filepath.Join(a.Cfg.DataDir, "tls"), "46.150.103.61"); e != nil {
		t.Fatal(e)
	}
	r = getProfile("/api/v1/enrollment?controller_url=" + url.QueryEscape(protocol.DefaultBootstrapURL+"/"))
	if !r.TLS.Matches || r.TLS.RouteVerified || r.CA != originalCA || a.ControllerID() != originalID || a.Cfg.AdvertisedURL != oldURL {
		t.Fatal("preflight changed identity/routes or claimed connectivity", r)
	}
	for _, bad := range []string{"http://example.test", "https://user:pass@example.test", "https://example.test/path", "https://example.test/?token=x", "https://example.test/#x"} {
		w := call("GET", "/api/v1/enrollment?controller_url="+url.QueryEscape(bad), nil)
		if w.Code != 400 {
			t.Fatal(bad, w.Code)
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/enrollment", nil))
	if w.Code != 401 {
		t.Fatal("profile accessible without login", w.Code)
	}
}

func TestV14InventoryListSelectorValidated(t *testing.T) {
	a, _ := testApp(t)
	w := httptest.NewRecorder()
	a.handleServices(w, httptest.NewRequest("GET", "/api/v1/services?inventory=delete", nil), nil)
	if w.Code != 400 {
		t.Fatal(w.Code, w.Body.String())
	}
}
