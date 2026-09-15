package server

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"github.com/alinescafs3mp-afk/monik_v2/internal/storage"
)

func TestV16HTTPBaselineAndCustomContract(t *testing.T) {
	for _, v := range []struct {
		code          int
		result, state string
	}{
		{200, "not_configured", "responds"}, {204, "not_configured", "responds"}, {302, "not_configured", "responds"},
		{401, "not_configured", "http_error"}, {403, "not_configured", "http_error"}, {404, "not_configured", "http_error"}, {503, "not_configured", "http_error"},
		{200, "fail", "app_fail"}, {503, "pass", "ok"},
	} {
		t.Run(fmt.Sprintf("%d_%s", v.code, v.result), func(t *testing.T) {
			a, _ := testApp(t)
			now := a.Clock.Now()
			if err := a.Store.InsertAgent(&storage.AgentRow{ID: "h", Hostname: "h", DesiredConfig: "{}"}, "hash"); err != nil {
				t.Fatal(err)
			}
			if err := a.Store.UpsertService(protocol.DiscoveredEndpoint{ServiceID: "svc", URL: "http://127.0.0.1:8000", DialTarget: "127.0.0.1:8000"}, "h"); err != nil {
				t.Fatal(err)
			}
			if _, err := a.Store.DB.Exec(`UPDATE agents SET last_live_at=?,desired_revision=1,applied_revision=1 WHERE id='h'`, now.UTC().Format(time.RFC3339Nano)); err != nil {
				t.Fatal(err)
			}
			obs := protocol.CheckObservation{ServiceID: "svc", CheckID: "check", ObservedAt: now, Transport: "ok", HTTPStatus: &v.code, Quality: protocol.QualityOK, AppResult: v.result, ConfigRev: 1, IntervalSeconds: 5}
			if err := a.Store.InsertCheckObs(obs, "h"); err != nil {
				t.Fatal(err)
			}
			a.evalCheck("h", obs)
			st, _, _, err := a.Store.State("service", "svc")
			expect := v.state
			if expect == "responds" {
				expect = "ok"
			}
			if err != nil || st != expect {
				t.Fatalf("evaluator %s %v want %s", st, err, expect)
			}
			rows, err := a.serviceSummaries("h", now)
			if err != nil || len(rows) != 1 {
				t.Fatal(rows, err)
			}
			if rows[0].State != v.state {
				t.Fatalf("summary %s want %s", rows[0].State, v.state)
			}
			if strings.Contains(rows[0].Summary, "здоровье приложения не настроено") {
				t.Fatal(rows[0].Summary)
			}
			b, _ := json.Marshal(rows[0])
			var dto map[string]any
			_ = json.Unmarshal(b, &dto)
			if dto["fresh_for_seconds"] != float64(20) {
				t.Fatalf("freshness absent: %s", b)
			}
		})
	}
}

func TestV16CheckJitterGraceExpires(t *testing.T) {
	if protocol.CheckFreshness(5) != 20*time.Second {
		t.Fatal("5s checks must use 20s grace")
	}
	if protocol.CheckFreshness(30) != 90*time.Second {
		t.Fatal("slow interval grace lost")
	}
}
