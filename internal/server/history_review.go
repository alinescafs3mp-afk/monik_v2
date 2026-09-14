package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"github.com/alinescafs3mp-afk/monik_v2/internal/secure"
	"github.com/alinescafs3mp-afk/monik_v2/internal/storage"
)

func (a *App) searchHistoricalIncidents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	from, e1 := time.Parse(time.RFC3339Nano, q.Get("from"))
	to, e2 := time.Parse(time.RFC3339Nano, q.Get("to"))
	limit := 100
	if q.Get("limit") != "" {
		var e error
		limit, e = strconv.Atoi(q.Get("limit"))
		if e != nil || limit < 1 || limit > 200 {
			a.writeErr(w, 400, "bad_limit", "limit must be 1..200")
			return
		}
	}
	if e1 != nil || e2 != nil || !to.After(from) || to.Sub(from) > 180*24*time.Hour {
		a.writeErr(w, 400, "bad_range", "use RFC3339 from/to, at most 180 days")
		return
	}
	state := q.Get("state")
	switch state {
	case "", "all", "open", "pending", "confirmed", "resolved", "interrupted":
	default:
		a.writeErr(w, 400, "bad_state", "invalid incident state")
		return
	}
	severity := q.Get("severity")
	if severity != "" && severity != "warning" && severity != "critical" {
		a.writeErr(w, 400, "bad_severity", "invalid severity")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	rows, next, err := a.Store.SearchIncidents(ctx, storage.IncidentQuery{From: from, To: to, State: state, Severity: severity, Metric: q.Get("metric"), EntityID: q.Get("entity_id"), Before: q.Get("cursor"), Limit: limit})
	if err != nil {
		status := 500
		if errors.Is(err, storage.ErrInvalidHistoryQuery) {
			status = 400
		}
		a.writeErr(w, status, "history_query_failed", "could not read incident history or cursor is invalid; no empty success returned")
		return
	}
	a.writeJSON(w, 200, map[string]any{"incidents": rows, "next_cursor": next, "from": from, "to": to, "view": "event_time_current_evidence", "server_time": a.Clock.Now()})
}

func (a *App) exportHistory(op *protocol.Operation, req protocol.SubmitOperation) error {
	fromText, _ := req.Params["from"].(string)
	toText, _ := req.Params["to"].(string)
	from, e1 := time.Parse(time.RFC3339Nano, fromText)
	to, e2 := time.Parse(time.RFC3339Nano, toText)
	if e1 != nil || e2 != nil || !to.After(from) || to.Sub(from) > 24*time.Hour {
		return fmt.Errorf("export requires RFC3339 from/to and a range up to 24 hours")
	}
	agent, _ := req.Params["agent_id"].(string)
	service, _ := req.Params["service_id"].(string)
	if (agent == "") == (service == "") {
		return fmt.Errorf("select exactly one agent_id or service_id")
	}
	kind, id := "agent", agent
	if service != "" {
		kind, id = "service", service
		if _, err := a.Store.Service(id); err != nil {
			return fmt.Errorf("service not found")
		}
	} else if _, err := a.Store.Agent(id); err != nil {
		return fmt.Errorf("agent not found")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rows, err := a.Store.ExportSamples(ctx, kind, id, from, to)
	if err != nil {
		return err
	}
	now := a.Clock.Now().UTC()
	artifact := map[string]any{"schema_version": 1, "generated_at": now, "entity_type": kind, "entity_id": id, "from": from, "to": to, "interval": "[from,to)", "precision": "raw", "view": "event_time_corrected", "count": len(rows), "samples": rows, "coverage_note": "Only retained received observations; gaps and expired data are not synthesized; this is not an uptime guarantee.", "units": map[string]string{"cpu_percent": "percent", "ram": "bytes", "disk": "bytes/percent", "temperature": "Celsius", "latency": "milliseconds"}}
	b, err := json.Marshal(artifact)
	if err != nil {
		return err
	}
	if len(b) > 16<<20 {
		return fmt.Errorf("export exceeds 16 MiB")
	}
	dir := filepath.Join(a.Cfg.DataDir, "exports")
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	// User history downloads are bounded temporary artifacts, not unlimited backups.
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var used int64
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if now.Sub(info.ModTime()) > 24*time.Hour {
			if err = os.Remove(filepath.Join(dir, entry.Name())); err != nil {
				return err
			}
			continue
		}
		used += info.Size()
	}
	if used+int64(len(b)) > 64<<20 {
		return fmt.Errorf("temporary export quota reached (64 MiB/24h); retry after expiry")
	}
	path := filepath.Join(dir, op.ID+".json")
	if err = secure.AtomicWrite(path, b, 0600); err != nil {
		return err
	}
	err = a.Store.UpdateTarget(op.ID, "server", protocol.TargetSucceeded, "bounded_artifact_ready", "Historical observations exported; download is available for 24 hours", "", false, map[string]any{"download_url": "/api/v1/exports/" + op.ID, "sha256": secure.SHA256Bytes(b), "size_bytes": len(b), "sample_count": len(rows), "expires_at": now.Add(24 * time.Hour).Format(time.RFC3339Nano), "from": from, "to": to, "precision": "raw"})
	if err != nil {
		_ = os.Remove(path)
	}
	return err
}
func (a *App) handleExportDownload(w http.ResponseWriter, r *http.Request, s *storage.Session) {
	id := r.PathValue("id")
	op, err := a.Store.Operation(id)
	if err != nil || op.Action != "history.export" || len(op.Targets) != 1 || op.Targets[0].Status != protocol.TargetSucceeded {
		a.writeErr(w, 404, "not_found", "completed export not found")
		return
	}
	if s.Role != "owner" && s.Username != op.Actor {
		a.writeErr(w, 403, "forbidden", "export belongs to another operator")
		return
	}
	exp, _ := op.Targets[0].Evidence["expires_at"].(string)
	until, err := time.Parse(time.RFC3339Nano, exp)
	if err != nil || !a.Clock.Now().Before(until) {
		a.writeErr(w, 410, "expired", "export expired; generate a new export")
		return
	}
	path, err := confinedJoin(filepath.Join(a.Cfg.DataDir, "exports"), id+".json")
	if err != nil {
		a.writeErr(w, 400, "bad_id", "invalid export id")
		return
	}
	file, err := os.Open(path)
	if err != nil {
		a.writeErr(w, 404, "not_found", "export file unavailable")
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 16<<20 {
		a.writeErr(w, 500, "integrity", "invalid export file")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(file, (16<<20)+1))
	if err != nil {
		a.writeErr(w, 404, "not_found", "export file unavailable")
		return
	}
	digest, _ := op.Targets[0].Evidence["sha256"].(string)
	if len(raw) > 16<<20 || secure.SHA256Bytes(raw) != digest {
		a.writeErr(w, 500, "integrity", "export verification failed")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", `attachment; filename="monik-history.json"`)
	_, _ = w.Write(raw)
}
