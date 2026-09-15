package server

import (
	"encoding/json"
	"github.com/alinescafs3mp-afk/monik_v2/internal/agent/configfile"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"testing"
	"time"
)

func TestV13OldCheckResultsCannotDescribeNewConfiguration(t *testing.T) {
	for _, result := range []string{"pass", "fail", "not_configured"} {
		t.Run(result, func(t *testing.T) {
			a, _ := testApp(t)
			audit7Inventory(t, a)
			d := protocol.CheckDefinition{ID: "check", ServiceID: "svc", URL: "http://127.0.0.1:8123/ready", Kind: "http_health", Method: "GET", ExpectedStatus: []int{200}, TimeoutSeconds: 2, IntervalSeconds: 5}
			cfg := protocol.DefaultAgentConfig()
			cfg.Checks = []protocol.CheckDefinition{d}
			raw, _ := json.Marshal(cfg)
			if e := a.Store.SetDesired("h", 2, configfile.HashConfig(cfg), string(raw)); e != nil {
				t.Fatal(e)
			}
			if e := a.Store.SetApplied("h", 2, configfile.HashConfig(cfg)); e != nil {
				t.Fatal(e)
			}
			if e := a.Store.TouchAgent("h", "session", 1, true, nil, nil, nil); e != nil {
				t.Fatal(e)
			}
			now := time.Now().UTC()
			code := 200
			for i := 0; i < 3; i++ {
				c := protocol.CheckObservation{ServiceID: "svc", CheckID: "check", ConfigRev: 1, ObservedAt: now.Add(time.Duration(i-3) * time.Second), Vantage: "agent/local", Transport: "ok", HTTPStatus: &code, AppResult: result, Quality: protocol.QualityOK, IntervalSeconds: 5}
				if e := a.Store.InsertCheckObs(c, "h"); e != nil {
					t.Fatal(e)
				}
				a.evalCheck("h", c)
			}
			summaries, e := a.serviceSummaries("h", now)
			if e != nil || len(summaries) != 1 || summaries[0].State != "pending" || summaries[0].Fresh {
				t.Fatalf("old result painted current: %+v %v", summaries, e)
			}
			var n int
			a.Store.DB.QueryRow(`SELECT count(*) FROM incidents WHERE entity_id='svc'`).Scan(&n)
			if n != 0 {
				t.Fatalf("old configuration opened %d incidents", n)
			}
		})
	}
}
