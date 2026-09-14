package server

import (
	"strings"
	"testing"
	"time"
)

func TestAudit8ServiceRenameSurvivesDiscoveryAndProtectsCAS(t *testing.T) {
	a, h := testApp(t)
	ep := audit7Inventory(t, a)
	a.ensureBaselineCheck("h", ep)
	before, _ := a.Store.Agent("h")
	call := audit6HTTP(t, a, h, time.Minute)
	w := call("POST", "/api/v1/operations", map[string]any{"action": "service.rename", "client_request_key": "rename-svc", "params": map[string]any{"service_id": "svc", "display_name": "Пятница API", "expected_name": ep.URL}})
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"completed"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	if e := a.Store.UpsertService(ep, "h"); e != nil {
		t.Fatal(e)
	}
	svc, e := a.Store.Service("svc")
	if e != nil || svc.DisplayName != "Пятница API" || svc.URL != ep.URL {
		t.Fatal(svc, e)
	}
	after, _ := a.Store.Agent("h")
	if before.DesiredHash != after.DesiredHash {
		t.Fatal("rename changed checks")
	}
	w = call("POST", "/api/v1/operations", map[string]any{"action": "service.rename", "client_request_key": "stale-rename-svc", "params": map[string]any{"service_id": "svc", "display_name": "stale", "expected_name": ep.URL}})
	if strings.Contains(w.Body.String(), `"status":"completed"`) {
		t.Fatal("stale rename succeeded")
	}
}
