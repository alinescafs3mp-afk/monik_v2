package discovery

import (
	"context"
	"encoding/json"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func endpoint(ts *httptest.Server) protocol.DiscoveredEndpoint {
	return protocol.DiscoveredEndpoint{ServiceID: "s", URL: ts.URL, DialTarget: strings.TrimPrefix(ts.URL, "http://"), SpeaksHTTP: true}
}
func TestAudit4HealthAdviceFindsRouteWithoutHidingUnready(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/readyz":
			w.WriteHeader(503)
			io.WriteString(w, `{"ready":false}`)
		case "/livez":
			io.WriteString(w, `{"status":"ok"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()
	advice := SuggestHealth(context.Background(), endpoint(ts), nil)
	if len(advice) != 2 || advice[0].Definition.Path != "/readyz" || advice[0].Health != "false" || !advice[0].AutoEligible {
		t.Fatalf("%+v", advice)
	}
	e := endpoint(ts)
	e.Suggestions = advice
	if err := protocol.ValidateSuggestions(e); err != nil {
		t.Fatal(err)
	}
}
func TestAudit4CatchAllHealthCannotAutoconfigure(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"status":"ok"}`)
	}))
	defer ts.Close()
	advice := SuggestHealth(context.Background(), endpoint(ts), nil)
	if len(advice) == 0 {
		t.Fatal("missing manual-review evidence")
	}
	for _, a := range advice {
		if a.AutoEligible {
			t.Fatal("catch-all blessed")
		}
	}
}
func TestAudit4AuthWallIsNotBypassed(t *testing.T) {
	var n atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { n.Add(1); w.WriteHeader(401) }))
	defer ts.Close()
	if a := SuggestHealth(context.Background(), endpoint(ts), nil); len(a) != 0 || n.Load() != 1 {
		t.Fatal("looked for alternate success behind auth wall")
	}
}
func TestAudit4Empty200AndHTMLAreNotHealth(t *testing.T) {
	for _, ct := range []string{"text/html", "application/json"} {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", ct)
			if ct == "text/html" {
				io.WriteString(w, "<h1>Login</h1>")
			}
		}))
		a := SuggestHealth(context.Background(), endpoint(ts), nil)
		ts.Close()
		for _, s := range a {
			if s.AutoEligible {
				t.Fatal("login/empty success auto treated as health")
			}
		}
	}
}
func TestAudit4AdvisorCacheAndWildcardFamilies(t *testing.T) {
	var n atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { n.Add(1); http.NotFound(w, r) }))
	defer ts.Close()
	a := &Advisor{}
	a.Suggest(context.Background(), endpoint(ts), nil)
	before := n.Load()
	a.Suggest(context.Background(), endpoint(ts), nil)
	if n.Load() != before {
		t.Fatal("repeated expensive advice before TTL")
	}
	a.Reset()
	a.Suggest(context.Background(), endpoint(ts), nil)
	if n.Load() <= before {
		t.Fatal("manual refresh not respected")
	}
	targets := DialTargets([]Listener{{IP: net.ParseIP("0.0.0.0"), Port: 8080}, {IP: net.ParseIP("::"), Port: 8081}}, []net.IP{net.ParseIP("192.168.1.1"), net.ParseIP("::1")})
	b, _ := json.Marshal(targets)
	if string(b) != `["127.0.0.1:8080","[::1]:8081"]` {
		t.Fatal(string(b))
	}
}
func TestAudit4KnownDatabaseIsNotSentHTTP(t *testing.T) {
	if !knownNonHTTP("redis-server") || knownNonHTTP("uvicorn") {
		t.Fatal("wrong process classifier")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	d := InspectListeners(ctx, []Listener{{IP: net.ParseIP("127.0.0.1"), Port: 12345}}, nil, 0, nil)
	if d.CoverageComplete || !d.Truncated {
		t.Fatal("budget falsely complete")
	}
}

func TestAudit4EmptyUnreadyCannotBeHiddenByLiveness(t *testing.T) {
	for _, status := range []int{503, 401, 403} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/readyz":
					w.WriteHeader(status)
				case "/livez":
					w.Header().Set("Content-Type", "application/json")
					io.WriteString(w, `{"status":"ok"}`)
				default:
					http.NotFound(w, r)
				}
			}))
			defer ts.Close()
			advice := SuggestHealth(context.Background(), endpoint(ts), nil)
			if len(advice) < 2 || advice[0].Definition.Path != "/readyz" {
				t.Fatalf("missing adverse evidence: %+v", advice)
			}
			for _, a := range advice {
				if a.AutoEligible {
					t.Fatal("automatic green bypassed uncertain readiness")
				}
			}
		})
	}
}
