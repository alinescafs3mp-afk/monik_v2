package discovery

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
)

func TestDisabledDiscoveryMakesNoRequests(t *testing.T) {
	var hits atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1); w.Write([]byte("OK")) }))
	defer s.Close()
	h, p, _ := net.SplitHostPort(s.Listener.Addr().String())
	port, _ := strconv.Atoi(p)
	d := protocol.CheckDefinition{ID: "check", ServiceID: "service", URL: s.URL, DialTarget: s.Listener.Addr().String(), Paused: true}
	block := DisabledTargets([]protocol.CheckDefinition{d}, nil)
	for i := 0; i < 3; i++ {
		out := InspectListenersPolicy(context.Background(), []Listener{{IP: net.ParseIP(h), Port: port}}, nil, 10, nil, block, true)
		if len(out.Confirmed) != 0 || len(out.Unresolved) != 1 {
			t.Fatal(out)
		}
	}
	if hits.Load() != 0 {
		t.Fatal("disabled service received discovery/health requests", hits.Load())
	}
}
func TestDisabledTargetsKeepIPv4IPv6AndEnabledSeparate(t *testing.T) {
	defs := []protocol.CheckDefinition{{ID: "v4", DialTarget: "0.0.0.0:8080", Paused: true}, {ID: "v6", DialTarget: "[::]:8080", Ignored: true}, {ID: "on", DialTarget: "127.0.0.1:9090"}, {ID: "url", URL: "https://127.0.0.1", Paused: true}}
	m := DisabledTargets(defs, nil)
	if len(m) != 3 || m["127.0.0.1:8080"].ID != "v4" || m["[::1]:8080"].ID != "v6" || m["127.0.0.1:443"].ID != "url" {
		t.Fatal(m)
	}
}
