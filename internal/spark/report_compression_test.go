package spark

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
)

func compressionReport() protocol.AgentReport {
	return protocol.AgentReport{SchemaVersion: 3, AgentID: "fixture-agent", AgentFlavor: "spark-light", SessionID: "immutable-session", Sequence: 1, ObservedAt: time.Now().UTC(), IsLive: true,
		Host:         &protocol.HostMetrics{Hostname: "fixture", RAMTotal: 100, RAMUsed: 10},
		Spark:        &protocol.SparkExtensionV1{SchemaVersion: 1, MemoryTopology: "unknown", Metrics: []protocol.SparkMetric{}},
		Capabilities: map[string]protocol.Capability{"compression_fixture": {Status: protocol.CapPartial, Reason: strings.Repeat("bounded report observation ", 256)}},
	}
}

func TestReportCompressionExactBytesAndStrictSavings(t *testing.T) {
	rep := compressionReport()
	plain, err := json.Marshal(rep)
	if err != nil {
		t.Fatal(err)
	}
	encoded, encoding, err := encodeReportBody(plain, true)
	if err != nil || encoding != "gzip" || len(encoded) >= len(plain) {
		t.Fatalf("compression failed: %d/%d %q %v", len(encoded), len(plain), encoding, err)
	}
	zip, err := gzip.NewReader(bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := io.ReadAll(zip)
	zip.Close()
	if err != nil || !bytes.Equal(decoded, plain) {
		t.Fatal("immutable JSON changed", err)
	}
	random := make([]byte, 8<<10)
	rand.New(rand.NewSource(17)).Read(random)
	for _, tc := range []struct {
		name    string
		data    []byte
		support bool
	}{{"unnegotiated", plain, false}, {"small", []byte(`{"small":true}`), true}, {"incompressible", random, true}} {
		t.Run(tc.name, func(t *testing.T) {
			got, encoding, err := encodeReportBody(tc.data, tc.support)
			if err != nil || encoding != "" || !bytes.Equal(got, tc.data) {
				t.Fatal("plain fallback changed bytes", err)
			}
		})
	}
	if _, _, err := encodeReportBody(make([]byte, maxReportBytes+1), true); err == nil {
		t.Fatal("oversized report compressed")
	}
}

func compressionWorker(t *testing.T, handler http.HandlerFunc) *Worker {
	t.Helper()
	w, _, _ := workerFixture(t, false)
	s := httptest.NewTLSServer(handler)
	t.Cleanup(s.Close)
	w.state.File.ControllerURL = s.URL
	w.client = s.Client()
	w.client.Timeout = 4 * time.Second
	return w
}

func TestReportCompressionNegotiatesOnlyVerifiedCommittedACK(t *testing.T) {
	for _, mode := range []string{"valid", "legacy", "wrong-controller", "wrong-sequence", "uncommitted", "duplicate-header", "ambiguous-header", "malformed-response", "historical"} {
		t.Run(mode, func(t *testing.T) {
			var mu sync.Mutex
			var encodings []string
			var bodies [][]byte
			rep := compressionReport()
			if mode == "historical" {
				rep.IsLive = false
			}
			w := compressionWorker(t, func(rw http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer fixture-credential" || r.Header.Get("X-Monik-Agent-Id") != rep.AgentID {
					t.Error("missing authentication")
				}
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
				}
				encoding := r.Header.Get("Content-Encoding")
				if encoding == "gzip" {
					zip, err := gzip.NewReader(bytes.NewReader(body))
					if err != nil {
						t.Error(err)
						return
					}
					body, err = io.ReadAll(zip)
					zip.Close()
					if err != nil {
						t.Error(err)
					}
				}
				mu.Lock()
				encodings = append(encodings, encoding)
				bodies = append(bodies, body)
				mu.Unlock()
				rw.Header().Set("Accept-Encoding", "gzip")
				cr := protocol.ControlResponse{ControllerID: "fixture-controller", Ack: &protocol.IngestAck{Committed: true, UpToSequence: rep.Sequence}}
				switch mode {
				case "legacy":
					rw.Header().Del("Accept-Encoding")
				case "wrong-controller":
					cr.ControllerID = "other-controller"
				case "wrong-sequence":
					cr.Ack.UpToSequence++
				case "uncommitted":
					cr.Ack.Committed = false
				case "duplicate-header":
					rw.Header().Add("Accept-Encoding", "gzip")
				case "ambiguous-header":
					rw.Header().Set("Accept-Encoding", "gzip, identity")
				case "malformed-response":
					io.WriteString(rw, `{"controller_id":"fixture-controller","controller_id":"fixture-controller"}`)
					return
				}
				json.NewEncoder(rw).Encode(cr)
			})
			for i := 0; i < 2; i++ {
				err := w.send(context.Background(), rep)
				invalid := mode == "wrong-controller" || mode == "wrong-sequence" || mode == "uncommitted" || mode == "malformed-response"
				if (err != nil) != invalid {
					t.Fatalf("ACK validation: %v", err)
				}
			}
			plain, _ := json.Marshal(rep)
			mu.Lock()
			defer mu.Unlock()
			if len(encodings) != 2 || encodings[0] != "" || !bytes.Equal(bodies[0], plain) || !bytes.Equal(bodies[1], plain) {
				t.Fatal("initial negotiation or immutable payload changed")
			}
			want := ""
			if mode == "valid" || mode == "historical" {
				want = "gzip"
			}
			if encodings[1] != want {
				t.Fatalf("encoding %q, want %q", encodings[1], want)
			}
		})
	}
}

func TestReportCompressionRollbackUsesNextNormalRetry(t *testing.T) {
	for _, status := range []int{400, 415, 501} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var mu sync.Mutex
			var encodings []string
			rep := compressionReport()
			w := compressionWorker(t, func(rw http.ResponseWriter, r *http.Request) {
				io.Copy(io.Discard, r.Body)
				mu.Lock()
				encodings = append(encodings, r.Header.Get("Content-Encoding"))
				n := len(encodings)
				mu.Unlock()
				if n == 2 {
					http.Error(rw, "old controller", status)
					return
				}
				if n == 1 {
					rw.Header().Set("Accept-Encoding", "gzip")
				}
				json.NewEncoder(rw).Encode(protocol.ControlResponse{ControllerID: "fixture-controller", Ack: &protocol.IngestAck{Committed: true, UpToSequence: rep.Sequence}})
			})
			if err := w.send(context.Background(), rep); err != nil {
				t.Fatal(err)
			}
			if err := w.send(context.Background(), rep); err == nil {
				t.Fatal("rollback rejection hidden")
			}
			mu.Lock()
			n := len(encodings)
			mu.Unlock()
			if n != 2 {
				t.Fatal("immediate resend bypassed normal spool retry")
			}
			if err := w.send(context.Background(), rep); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			defer mu.Unlock()
			if strings.Join(encodings, ",") != ",gzip," {
				t.Fatal("rollback did not clear capability", encodings)
			}
		})
	}
}

func TestReportCompressionDoesNotEncodeOtherAuthenticatedPaths(t *testing.T) {
	w := compressionWorker(t, func(rw http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Encoding") != "" {
			t.Error("non-report request compressed")
		}
		io.Copy(io.Discard, r.Body)
		rw.WriteHeader(200)
	})
	w.reportGzip = true
	for _, path := range []string{"/api/v1/agent/spark/stage", "/api/v1/agent/spark/logs"} {
		response, err := w.authenticatedRequest(context.Background(), http.MethodPost, path, map[string]string{"data": strings.Repeat("fixture", 2048)})
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
	}
}
