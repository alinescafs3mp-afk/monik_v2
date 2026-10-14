package server

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/agent/configfile"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"github.com/alinescafs3mp-afk/monik_v2/internal/secure"
	"github.com/alinescafs3mp-afk/monik_v2/internal/spark"
	"github.com/alinescafs3mp-afk/monik_v2/internal/storage"
)

func gzipReportTestBody(t *testing.T, data []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	zip := gzip.NewWriter(&out)
	if _, err := zip.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := zip.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestBoundedReportCodecRejectsAmbiguousOrIncompleteBodies(t *testing.T) {
	plain := []byte(`{"identity":"fixture","sequence":1}`)
	encoded := gzipReportTestBody(t, plain)
	checksum := append([]byte(nil), encoded...)
	checksum[len(checksum)-8] ^= 1
	length := append([]byte(nil), encoded...)
	length[len(length)-1] ^= 1
	limitJSON := append([]byte(`{"x":"`), bytes.Repeat([]byte("x"), maxReportBytes-8)...)
	limitJSON = append(limitJSON, []byte(`"}`)...)
	for _, tc := range []struct {
		name     string
		body     []byte
		encoding []string
		valid    bool
	}{
		{"plain legacy", plain, nil, true},
		{"identity", plain, []string{"identity"}, true},
		{"gzip", encoded, []string{"gzip"}, true},
		{"case", encoded, []string{"GZip"}, true},
		{"decoded exact limit", gzipReportTestBody(t, limitJSON), []string{"gzip"}, true},
		{"unknown encoding", encoded, []string{"br"}, false},
		{"stacked encoding", encoded, []string{"gzip, identity"}, false},
		{"duplicate encoding", encoded, []string{"gzip", "gzip"}, false},
		{"missing encoding", encoded, nil, false},
		{"plain labeled gzip", plain, []string{"gzip"}, false},
		{"truncated header", encoded[:5], []string{"gzip"}, false},
		{"truncated trailer", encoded[:len(encoded)-1], []string{"gzip"}, false},
		{"checksum", checksum, []string{"gzip"}, false},
		{"length", length, []string{"gzip"}, false},
		{"second gzip member", append(append([]byte(nil), encoded...), encoded...), []string{"gzip"}, false},
		{"empty second member", append(append([]byte(nil), encoded...), gzipReportTestBody(t, nil)...), []string{"gzip"}, false},
		{"trailing byte", append(append([]byte(nil), encoded...), 0), []string{"gzip"}, false},
		{"trailing whitespace", append(append([]byte(nil), encoded...), ' '), []string{"gzip"}, false},
		{"duplicate JSON member", gzipReportTestBody(t, []byte(`{"sequence":1,"sequence":2}`)), []string{"gzip"}, false},
		{"trailing JSON", gzipReportTestBody(t, append(append([]byte(nil), plain...), plain...)), []string{"gzip"}, false},
		{"JSON array", gzipReportTestBody(t, []byte(`[]`)), []string{"gzip"}, false},
		{"decoded bomb", gzipReportTestBody(t, append(limitJSON, ' ')), []string{"gzip"}, false},
		{"encoded oversize", make([]byte, maxReportBytes+1), []string{"gzip"}, false},
		{"plain oversize", append(limitJSON, ' '), nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/api/v1/agent/report", bytes.NewReader(tc.body))
			for _, value := range tc.encoding {
				r.Header.Add("Content-Encoding", value)
			}
			var dst map[string]any
			if err := parseReportJSON(r, &dst); (err == nil) != tc.valid {
				t.Fatalf("valid=%v, err=%v", tc.valid, err)
			}
		})
	}
}

type reportReadCallback struct {
	io.Reader
	read func()
}

func (r *reportReadCallback) Read(p []byte) (int, error) {
	if r.read != nil {
		r.read()
		r.read = nil
	}
	return r.Reader.Read(p)
}

func TestBoundedReportCodecAuthenticationAndCommitBoundary(t *testing.T) {
	for _, mode := range []string{"unauthenticated", "wrong-credential", "revoked-during-read", "wrong-agent", "malformed", "storage-rejected", "plain", "gzip"} {
		t.Run(mode, func(t *testing.T) {
			f := newSparkAPI(t, true)
			rep := f.report
			rep.Sequence++
			if mode == "wrong-agent" {
				rep.AgentID = "other-agent"
			}
			if mode == "storage-rejected" {
				rep.Sequence = 0
			}
			plain, _ := json.Marshal(rep)
			body := gzipReportTestBody(t, plain)
			if mode == "plain" {
				body = plain
			}
			if mode == "malformed" {
				body = body[:len(body)-1]
			}
			read := false
			reader := &reportReadCallback{Reader: bytes.NewReader(body), read: func() {
				read = true
				if mode == "revoked-during-read" {
					if err := f.app.Store.RevokeAgent(f.report.AgentID); err != nil {
						t.Fatal(err)
					}
				}
			}}
			r := httptest.NewRequest(http.MethodPost, "/api/v1/agent/report", reader)
			if mode != "unauthenticated" {
				credential := "credential"
				if mode == "wrong-credential" {
					credential = "wrong"
				}
				r.Header.Set("Authorization", "Bearer "+credential)
				r.Header.Set("X-Monik-Agent-Id", f.report.AgentID)
			}
			if mode != "plain" {
				r.Header.Set("Content-Encoding", "gzip")
			}
			w := httptest.NewRecorder()
			f.handler.ServeHTTP(w, r)
			want := map[string]int{"unauthenticated": 401, "wrong-credential": 401, "revoked-during-read": 401, "wrong-agent": 403, "malformed": 400, "storage-rejected": 409, "plain": 200, "gzip": 200}[mode]
			if w.Code != want {
				t.Fatalf("status %d, want %d", w.Code, want)
			}
			if (mode == "unauthenticated" || mode == "wrong-credential") && read {
				t.Fatal("unauthenticated body decompressed/read")
			}
			var count int
			if err := f.app.Store.DB.QueryRow(`SELECT count(*) FROM ingest_receipts WHERE agent_id=?`, f.report.AgentID).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != 1+map[bool]int{true: 1}[want == 200] {
				t.Fatal("failed request committed report", count)
			}
			if (w.Header().Get("Accept-Encoding") == "gzip") != (want == 200) {
				t.Fatal("capability advertised without committed ACK")
			}
		})
	}
}

// Exercise the native worker, real TLS HTTP route, bounded decoder and storage
// together, using only a private temporary enrollment and no broker/model jobs.
func TestBoundedReportCodecNativeWorkerHTTPStorageEquivalence(t *testing.T) {
	app, handler := testApp(t)
	if err := app.Store.InsertAgent(&storage.AgentRow{ID: "compression-worker"}, secure.HashToken("fixture-credential")); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	var mu sync.Mutex
	var reports []protocol.AgentReport
	var encodings []string
	var testErr error
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/agent/report" {
			handler.ServeHTTP(w, r)
			return
		}
		encoded, err := io.ReadAll(io.LimitReader(r.Body, maxReportBytes+1))
		if err != nil {
			t.Error(err)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(encoded))
		var rep protocol.AgentReport
		if err := parseReportJSON(r, &rep); err != nil {
			t.Error(err)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(encoded))
		rw := httptest.NewRecorder()
		handler.ServeHTTP(rw, r)
		mu.Lock()
		if rw.Code != 200 {
			testErr = io.ErrUnexpectedEOF
		}
		reports = append(reports, rep)
		encodings = append(encodings, r.Header.Get("Content-Encoding"))
		n := len(reports)
		mu.Unlock()
		for key, values := range rw.Header() {
			w.Header()[key] = values
		}
		w.WriteHeader(rw.Code)
		w.Write(rw.Body.Bytes())
		if n == 2 {
			cancel()
		}
	}))
	defer ts.Close()
	cert, err := x509.ParseCertificate(ts.TLS.Certificates[0].Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	credential := filepath.Join(dir, "credential")
	if err := os.WriteFile(credential, []byte("fixture-credential"), 0600); err != nil {
		t.Fatal(err)
	}
	state := configfile.File{SchemaVersion: 3, AgentID: "compression-worker", ControllerID: app.ControllerID(), ControllerURL: ts.URL, CredentialPath: credential, StateDir: dir, EndpointGeneration: 1,
		CACertPEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}))}
	config, _ := json.Marshal(state)
	path := filepath.Join(dir, "agent.json")
	if err := os.WriteFile(path, config, 0600); err != nil {
		t.Fatal(err)
	}
	// Pausing stops check/discovery scheduling; the normal five-second report
	// cadence and native capability envelope still exercise compression.
	cfg := protocol.DefaultAgentConfig()
	cfg.Paused = true
	if err := configfile.SaveAppliedConfig(dir, cfg, 1, configfile.HashConfig(cfg)); err != nil {
		t.Fatal(err)
	}
	worker, err := spark.OpenWorker(path, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := worker.Run(ctx); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if testErr != nil || len(reports) != 2 || encodings[0] != "" || encodings[1] != "gzip" {
		t.Fatalf("native negotiation/storage failed: reports=%d encodings=%q err=%v", len(reports), encodings, testErr)
	}
	for _, rep := range reports {
		observation, err := protocol.ObservationPayload(rep)
		if err != nil {
			t.Fatal(err)
		}
		var hash string
		if err := app.Store.DB.QueryRow(`SELECT payload_hash FROM ingest_receipts WHERE agent_id=? AND session_id=? AND seq=?`, rep.AgentID, rep.SessionID, rep.Sequence).Scan(&hash); err != nil {
			t.Fatal(err)
		}
		if hash != secure.SHA256Bytes(observation) {
			t.Fatal("worker JSON changed before immutable storage commitment")
		}
	}
}

func TestBoundedReportCodecCompressedReplayPreservesHistory(t *testing.T) {
	f := newSparkAPI(t, true)
	rep := f.report
	rep.Sequence++
	plain, _ := json.Marshal(rep)
	for _, encoded := range []bool{false, true, true} {
		body := plain
		if encoded {
			body = gzipReportTestBody(t, plain)
		}
		r := httptest.NewRequest(http.MethodPost, "/api/v1/agent/report", bytes.NewReader(body))
		r.Header.Set("Authorization", "Bearer credential")
		r.Header.Set("X-Monik-Agent-Id", rep.AgentID)
		if encoded {
			r.Header.Set("Content-Encoding", "gzip")
		}
		w := httptest.NewRecorder()
		f.handler.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatal("immutable plain/gzip replay rejected", w.Code)
		}
		// Change only replay metadata permitted by the existing observation hash.
		rep.IsLive = false
		rep.ReportedAt = f.app.Clock.Now()
		plain, _ = json.Marshal(rep)
	}
	var count int
	if err := f.app.Store.DB.QueryRow(`SELECT count(*) FROM spark_samples WHERE agent_id=?`, rep.AgentID).Scan(&count); err != nil || count != 2 {
		t.Fatal("compressed duplicate created extra history", count, err)
	}
	var payload string
	if err := f.app.Store.DB.QueryRow(`SELECT payload FROM spark_samples WHERE agent_id=? AND session_id=? AND seq=?`, rep.AgentID, rep.SessionID, rep.Sequence).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(rep.Spark)
	if strings.TrimSpace(payload) != string(want) {
		t.Fatal("stored Spark samples changed")
	}
}
