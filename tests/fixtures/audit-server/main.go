// This loopback-only fixture serves the real application with synthetic telemetry.
// It never connects to the owner's deployment and does not install an OS service.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/idgen"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"github.com/alinescafs3mp-afk/monik_v2/internal/secure"
	"github.com/alinescafs3mp-afk/monik_v2/internal/server"
	"github.com/alinescafs3mp-afk/monik_v2/internal/storage"
)

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
func ptr(v float64) *float64 { return &v }
func host(at time.Time, i int) *protocol.HostMetrics {
	cpu := 42.0
	if i%400 == 50 {
		cpu = 99
	}
	return &protocol.HostMetrics{Hostname: "fixture-linux", DisplayName: "Audit host", OS: "linux", OSVersion: "fixture", Arch: "amd64", CPUModel: "Fixture CPU", CPULogical: 8, CPUPercent: &cpu, RAMTotal: 16 << 30, RAMUsed: 6 << 30, RAMAvailable: 10 << 30, SystemUptime: 86400, AgentUptime: 3600, Disks: []protocol.Disk{{Mount: "/", FS: "ext4", Total: 100 << 30, Used: 65 << 30, Available: 35 << 30, UsedPct: 65}, {Mount: "/data", FS: "ext4", Total: 200 << 30, Used: 190 << 30, Available: 10 << 30, UsedPct: 95}}, Ping: &protocol.PingSummary{Target: "8.8.8.8", MeanMS: ptr(23.4), MinMS: ptr(20), MaxMS: ptr(28), Sent: 12, Received: 12, LossPct: ptr(0), WindowSec: 60}, Temperatures: []protocol.Temperature{{Source: "fixture", Label: "CPU package", Celsius: ptr(61), Observed: at, Quality: protocol.QualityOK}}}
}
func main() {
	dir := flag.String("data-dir", "", "new empty test directory, required")
	port := flag.Int("port", 18777, "loopback port")
	flag.Parse()
	if *dir == "" {
		log.Fatal("--data-dir is required")
	}
	if _, err := os.Stat(filepath.Join(*dir, "monik.db")); err == nil {
		log.Fatal("fixture requires a new empty database")
	}
	addr := fmt.Sprintf("127.0.0.1:%d", *port)
	url := "https://" + addr
	app, err := server.Open(server.Config{DataDir: *dir, Listen: addr, AdvertisedURL: url})
	must(err)
	defer app.Close()
	password, err := idgen.Secret(18)
	must(err)
	must(app.CompleteSetup(server.SetupRequest{Username: "owner", Password: password, Listen: addr, AdvertisedURL: url}))
	cfg := protocol.DefaultAgentConfig()
	raw, _ := json.Marshal(cfg)
	hash := secure.SHA256Bytes(raw)
	for _, id := range []string{"audit-host", "offline-host"} {
		must(app.Store.InsertAgent(&storage.AgentRow{ID: id, Hostname: id, DisplayName: map[string]string{"audit-host": "Audit host", "offline-host": "Offline fixture"}[id], OS: "linux", Arch: "amd64", DesiredRevision: 1, DesiredHash: hash, DesiredConfig: string(raw)}, "fixture-verifier"))
		must(app.Store.SetDesired(id, 1, hash, string(raw)))
	}
	services := []struct {
		id, name       string
		code           int
		result, reason string
	}{{"audit-api", "API", 200, "pass", ""}, {"audit-auth", "Protected panel", 401, "not_configured", ""}, {"audit-broken", "Worker UI", 503, "fail", "status 503 not in expected"}}
	for i, sv := range services {
		target := fmt.Sprintf("127.0.0.1:%d", 28000+i)
		must(app.Store.UpsertService(protocol.DiscoveredEndpoint{ServiceID: sv.id, DialTarget: target, URL: "http://" + target, SpeaksHTTP: true, Source: "fixture"}, "audit-host"))
		must(app.Store.UpdateServiceFlags(sv.id, map[string]any{"display_name": sv.name}))
	}
	now := time.Now().UTC()
	for i := 0; i < 1200; i++ {
		at := now.Add(time.Duration(i-1200) * 5 * time.Second)
		must(app.Store.InsertHostSample("audit-host", int64(i+1), "history", at, host(at, i)))
	}
	feed := func(seq int64) {
		at := time.Now().UTC()
		rep := protocol.AgentReport{SchemaVersion: 3, AgentID: "audit-host", SessionID: "fixture-live", Sequence: seq, ObservedAt: at, IsLive: true, ConfigRevision: 1, ConfigHash: hash, WorkerVersion: "audit-fixture", Capabilities: map[string]protocol.Capability{"http_custom_v1": {Status: "supported"}}, Host: host(at, int(seq))}
		for _, sv := range services {
			code := sv.code
			rep.Checks = append(rep.Checks, protocol.CheckObservation{ServiceID: sv.id, CheckID: "check-" + sv.id, ObservedAt: at, Vantage: "agent/local", Transport: "ok", HTTPStatus: &code, LatencyMS: ptr(12), AppResult: sv.result, AppReason: sv.reason, Quality: protocol.QualityOK})
		}
		_, err := app.Store.AcceptReport(rep)
		must(err)
		must(app.Store.AppendEvent("metrics", "agent", "audit-host", seq, map[string]any{"fixture": true}))
	}
	feed(1)
	access, _ := json.Marshal(map[string]string{"url": url, "username": "owner", "password": password})
	must(os.WriteFile(filepath.Join(*dir, "fixture-access.json"), access, 0600))
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		seq := int64(1)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				seq++
				feed(seq)
			}
		}
	}()
	log.Printf("Fixture ready: %s; access file: %s", url, filepath.Join(*dir, "fixture-access.json"))
	if err := app.Run(ctx); err != nil && ctx.Err() == nil {
		log.Fatal(err)
	}
}
