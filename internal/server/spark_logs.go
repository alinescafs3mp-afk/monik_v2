package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/alinescafs3mp-afk/monik_v2/internal/idgen"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"github.com/alinescafs3mp-afk/monik_v2/internal/storage"
)

// Only locally sanitized complete records reach these bounded ephemeral
// buffers. Model log content is never written to SQLite or controller logs.
type sparkLogState struct {
	mu      sync.Mutex
	streams map[string]*sparkLogStream
}
type sparkLogStream struct {
	id, agent           string
	request             protocol.SparkLogRequest
	expires, leaseUntil time.Time
	owners              map[string]*storage.Session
	viewers             map[string]*sparkLogViewer
	records             []protocol.SparkLogRecord
	bytes               int
	lastSequence        int64
	closed              string
}
type sparkLogViewer struct {
	session *storage.Session
	batches chan []protocol.SparkLogRecord
	done    chan struct{}
	reason  string
}

func logParams(p protocol.SparkLogRequest) protocol.ModelActionParams {
	return protocol.ModelActionParams{ProfileID: p.ProfileID, ProfileRevision: p.ProfileRevision, ExpectedGeneration: p.ExpectedGeneration, ControlEpoch: p.ControlEpoch, ExpectedAgentSession: p.ExpectedAgentSession, ConfirmDisruption: true}
}
func (a *App) logRequestValid(id string, p protocol.SparkLogRequest) error {
	b, _ := json.Marshal(logParams(p))
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	if _, e := protocol.DecodeModelActionParams(m); e != nil {
		return e
	}
	// Log access is separately authorized and does not require a permitted
	// lifecycle action. It still binds the approved current profile and fence.
	v, e := a.Store.SparkSnapshot(id)
	if e != nil {
		return e
	}
	ag, e := a.Store.Agent(id)
	if e != nil {
		return e
	}
	if !a.sparkOnline(ag, v) || ag.SessionID != p.ExpectedAgentSession {
		return fmt.Errorf("agent_offline_or_session_changed")
	}
	var caps map[string]protocol.Capability
	if e = json.Unmarshal([]byte(ag.Capabilities), &caps); e != nil || caps["spark_logs_v1"].Status != protocol.CapSupported {
		return fmt.Errorf("model_logs_unsupported")
	}
	fresh := func(at time.Time) bool {
		age := a.Clock.Now().Sub(at)
		return age >= -5*time.Second && age <= protocol.SparkControlFreshness
	}
	c := v.Spark.ControlState
	if c == nil || c.Epoch == nil || *c.Epoch != p.ControlEpoch || !c.Enabled || !fresh(c.ObservedAt) {
		return fmt.Errorf("stale_or_disabled_control")
	}
	profile := false
	if v.Spark.Inventory != nil {
		for _, p2 := range v.Spark.Inventory.Profiles {
			if p2.ProfileID == p.ProfileID && p2.ProfileRevision == p.ProfileRevision && p2.LogAllowed {
				profile = true
			}
		}
	}
	if !profile {
		return fmt.Errorf("local_log_source_not_approved")
	}
	for _, st := range v.Spark.ModelStates {
		if st.ProfileID == p.ProfileID && st.Generation == p.ExpectedGeneration && st.Quality == "ok" && fresh(st.ObservedAt) {
			return nil
		}
	}
	return fmt.Errorf("stale_generation_or_state")
}
func (a *App) logOwnerValid(s *storage.Session) bool {
	v, e := a.Store.RevalidateSession(s)
	return e == nil && v.Role == "owner" && recentOK(v, a.Clock.Now())
}
func (a *App) closeLogStream(st *sparkLogStream, reason string) {
	if st.closed != "" {
		return
	}
	st.closed = reason
	st.records = nil
	st.bytes = 0
	for _, v := range st.viewers {
		v.reason = reason
		close(v.done)
	}
	st.viewers = map[string]*sparkLogViewer{}
}
func (a *App) closeSparkLogs() {
	a.sparkLogs.mu.Lock()
	defer a.sparkLogs.mu.Unlock()
	for _, st := range a.sparkLogs.streams {
		a.closeLogStream(st, "controller_closed")
	}
	a.sparkLogs.streams = nil
}
func (a *App) pruneLogStreamsLocked() {
	for id, st := range a.sparkLogs.streams {
		if !a.Clock.Now().Before(st.expires) {
			a.closeLogStream(st, "expired")
		}
		if st.closed != "" {
			delete(a.sparkLogs.streams, id)
		}
	}
}
func (a *App) handleSparkLogCreate(w http.ResponseWriter, r *http.Request, s *storage.Session) {
	var p protocol.SparkLogRequest
	if e := parseJSONStrictLimit(r, &p, 4096); e != nil {
		a.writeErr(w, 400, "bad_params", "typed log binding required")
		return
	}
	s = a.currentOwner(w, s, true)
	if s == nil {
		return
	}
	id := r.PathValue("id")
	if e := a.logRequestValid(id, p); e != nil {
		a.writeErr(w, 409, "log_source_unavailable", e.Error())
		return
	}
	a.sparkLogs.mu.Lock()
	defer a.sparkLogs.mu.Unlock()
	if a.sparkLogs.streams == nil {
		a.sparkLogs.streams = map[string]*sparkLogStream{}
	}
	a.pruneLogStreamsLocked()
	active := 0
	for _, st := range a.sparkLogs.streams {
		if st.agent != id {
			continue
		}
		active++
		if st.request == p {
			st.owners[s.ID] = s
			a.writeJSON(w, 200, map[string]any{"stream_id": st.id, "expires_at": st.expires})
			return
		}
	}
	if active >= 4 {
		a.writeErr(w, 429, "log_limit", "at most four active profile sources per host")
		return
	}
	if len(a.sparkLogs.streams) >= 256 {
		a.writeErr(w, 429, "log_limit", "controller log stream limit reached")
		return
	}
	st := &sparkLogStream{id: idgen.New(), agent: id, request: p, expires: a.Clock.Now().Add(5 * time.Minute), owners: map[string]*storage.Session{s.ID: s}, viewers: map[string]*sparkLogViewer{}}
	a.sparkLogs.streams[st.id] = st
	a.Store.Audit(s.Username, "spark.logs", id, p.ProfileID)
	a.writeJSON(w, 201, map[string]any{"stream_id": st.id, "expires_at": st.expires})
}
func (a *App) handleSparkLogDelete(w http.ResponseWriter, r *http.Request, s *storage.Session) {
	s = a.currentOwner(w, s, false)
	if s == nil {
		return
	}
	a.sparkLogs.mu.Lock()
	defer a.sparkLogs.mu.Unlock()
	st := a.sparkLogs.streams[r.PathValue("stream")]
	if st == nil || st.agent != r.PathValue("id") {
		a.writeErr(w, 404, "not_found", "stream unavailable")
		return
	}
	delete(st.owners, s.ID)
	for vid, v := range st.viewers {
		if v.session.ID == s.ID {
			v.reason = "closed_by_owner"
			close(v.done)
			delete(st.viewers, vid)
		}
	}
	if len(st.owners) == 0 {
		a.closeLogStream(st, "closed_by_owner")
		delete(a.sparkLogs.streams, st.id)
	}
	a.writeJSON(w, 200, map[string]any{"closed": true})
}
func logTail(records []protocol.SparkLogRecord) []protocol.SparkLogRecord {
	count, bytes := 0, 0
	start := len(records)
	for start > 0 && count < 200 {
		size := len(records[start-1].Text)
		if bytes+size > 128<<10 {
			break
		}
		start--
		count++
		bytes += size
	}
	return append([]protocol.SparkLogRecord(nil), records[start:]...)
}
func (a *App) handleSparkLogEvents(w http.ResponseWriter, r *http.Request, s *storage.Session) {
	s = a.currentOwner(w, s, true)
	if s == nil {
		return
	}
	release, available := a.eventStreamSlot(s.ID)
	if !available {
		a.writeErr(w, 429, "stream_limit", "live stream capacity reached")
		return
	}
	defer release()
	f, ok := w.(http.Flusher)
	if !ok {
		a.writeErr(w, 500, "stream_unsupported", "stream unavailable")
		return
	}
	a.sparkLogs.mu.Lock()
	a.pruneLogStreamsLocked()
	st := a.sparkLogs.streams[r.PathValue("stream")]
	if st == nil || st.agent != r.PathValue("id") || st.owners[s.ID] == nil {
		a.sparkLogs.mu.Unlock()
		a.writeErr(w, 404, "not_found", "stream unavailable")
		return
	}
	viewers := 0
	for _, source := range a.sparkLogs.streams {
		if source.agent == st.agent {
			viewers += len(source.viewers)
		}
	}
	if viewers >= 16 {
		a.sparkLogs.mu.Unlock()
		a.writeErr(w, 429, "log_viewer_limit", "at most sixteen viewers per host")
		return
	}
	vid := idgen.New()
	v := &sparkLogViewer{session: s, batches: make(chan []protocol.SparkLogRecord, 8), done: make(chan struct{})}
	st.viewers[vid] = v
	tail := logTail(st.records)
	a.sparkLogs.mu.Unlock()
	defer func() {
		a.sparkLogs.mu.Lock()
		delete(st.viewers, vid)
		found := false
		for _, viewer := range st.viewers {
			if viewer.session.ID == s.ID {
				found = true
			}
		}
		if !found {
			delete(st.owners, s.ID)
		}
		if len(st.owners) == 0 {
			a.closeLogStream(st, "no_viewers")
			delete(a.sparkLogs.streams, st.id)
		}
		a.sparkLogs.mu.Unlock()
	}()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	write := func(event string, p any) bool {
		b, _ := json.Marshal(p)
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(5 * time.Second))
		_, e := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
		if e == nil {
			f.Flush()
		}
		return e == nil
	}
	if !a.logOwnerValid(s) {
		return
	}
	for _, rec := range tail {
		if !write("log", rec) {
			return
		}
	}
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-v.done:
			write("closed", map[string]any{"reason": v.reason})
			return
		case batch := <-v.batches:
			if !a.logOwnerValid(s) {
				write("closed", map[string]any{"reason": "authority_revoked"})
				return
			}
			for _, rec := range batch {
				if !write("log", rec) {
					return
				}
			}
		case <-ticker.C:
			if !a.logOwnerValid(s) {
				write("closed", map[string]any{"reason": "authority_revoked"})
				return
			}
			if !a.Clock.Now().Before(st.expires) || a.logRequestValid(st.agent, st.request) != nil {
				write("closed", map[string]any{"reason": "source_changed_or_expired"})
				return
			}
			if !write("keepalive", map[string]any{"at": a.Clock.Now()}) {
				return
			}
		}
	}
}
func (a *App) handleAgentSparkLogLeases(w http.ResponseWriter, r *http.Request) {
	a.controlMu.Lock()
	defer a.controlMu.Unlock()
	ag, _, ok := a.agentFromCurrent(r)
	if !ok {
		a.writeErr(w, 401, "unauthenticated", "current agent credential required")
		return
	}
	a.sparkLogs.mu.Lock()
	defer a.sparkLogs.mu.Unlock()
	a.pruneLogStreamsLocked()
	out := []protocol.SparkLogLease{}
	for _, st := range a.sparkLogs.streams {
		if st.agent != ag.ID {
			continue
		}
		valid := false
		for sid, owner := range st.owners {
			if a.logOwnerValid(owner) {
				valid = true
			} else {
				delete(st.owners, sid)
			}
		}
		if !valid || a.Cfg.RestoreMode || a.logRequestValid(st.agent, st.request) != nil {
			a.closeLogStream(st, "authority_or_source_changed")
			delete(a.sparkLogs.streams, st.id)
			continue
		}
		st.leaseUntil = a.Clock.Now().Add(protocol.SparkDispatchLease)
		if st.expires.Before(st.leaseUntil) {
			st.leaseUntil = st.expires
		}
		out = append(out, protocol.SparkLogLease{StreamID: st.id, Request: st.request, Deadline: st.leaseUntil, TailLines: 200, TailBytes: 128 << 10})
	}
	w.Header().Set("Cache-Control", "no-store")
	a.writeJSON(w, 200, map[string]any{"leases": out, "server_time": a.Clock.Now()})
}
func (a *App) handleAgentSparkLogBatch(w http.ResponseWriter, r *http.Request) {
	var batch protocol.SparkLogBatch
	if e := parseJSONStrictLimit(r, &batch, 192<<10); e != nil || len(batch.Records) > 200 {
		a.writeErr(w, 400, "bad_logs", "bounded complete filtered records required")
		return
	}
	total := 0
	for _, rec := range batch.Records {
		if rec.Sequence < 1 || rec.At.IsZero() || len(rec.Text) > 16<<10 || !utf8.ValidString(rec.Text) || strings.ContainsAny(rec.Text, "\r\n") {
			a.writeErr(w, 400, "bad_logs", "invalid complete log record")
			return
		}
		for _, c := range rec.Text {
			if c < 32 && c != '\t' || c == 127 {
				a.writeErr(w, 400, "bad_logs", "control characters must be filtered locally")
				return
			}
		}
		total += len(rec.Text)
	}
	if total > 128<<10 || len(batch.Reason) > 256 {
		a.writeErr(w, 400, "bad_logs", "log batch exceeds budget")
		return
	}
	a.controlMu.Lock()
	defer a.controlMu.Unlock()
	ag, _, ok := a.agentFromCurrent(r)
	if !ok {
		a.writeErr(w, 401, "unauthenticated", "current agent credential required")
		return
	}
	a.sparkLogs.mu.Lock()
	defer a.sparkLogs.mu.Unlock()
	st := a.sparkLogs.streams[batch.StreamID]
	if st == nil || st.agent != ag.ID || st.request.ExpectedAgentSession != batch.AgentSession || ag.SessionID != batch.AgentSession || !a.Clock.Now().Before(st.leaseUntil) {
		a.writeErr(w, 409, "log_lease_expired", "stream lease is unavailable")
		return
	}
	valid := false
	for _, owner := range st.owners {
		if a.logOwnerValid(owner) {
			valid = true
		}
	}
	if !valid || a.logRequestValid(st.agent, st.request) != nil {
		a.closeLogStream(st, "authority_or_source_changed")
		a.writeErr(w, 403, "log_authority_revoked", "log authority changed")
		return
	}
	accepted := []protocol.SparkLogRecord{}
	for _, rec := range batch.Records {
		if rec.Sequence <= st.lastSequence {
			continue
		}
		if st.lastSequence > 0 && rec.Sequence != st.lastSequence+1 {
			rec.Gap = true
		}
		st.lastSequence = rec.Sequence
		st.records = append(st.records, rec)
		st.bytes += len(rec.Text)
		accepted = append(accepted, rec)
	}
	for st.bytes > 256<<10 || len(st.records) > 4096 {
		st.bytes -= len(st.records[0].Text)
		st.records = st.records[1:]
	}
	// Queue whole record groups of at most 16 KiB. Slow viewers have at
	// most eight groups (128 KiB), then are disconnected with a reason.
	for len(accepted) > 0 {
		n, bytes := 0, 0
		for n < len(accepted) {
			size := len(accepted[n].Text)
			if n > 0 && bytes+size > 16<<10 {
				break
			}
			bytes += size
			n++
		}
		group := append([]protocol.SparkLogRecord(nil), accepted[:n]...)
		for id, v := range st.viewers {
			select {
			case v.batches <- group:
			default:
				v.reason = "slow_viewer_gap"
				close(v.done)
				delete(st.viewers, id)
			}
		}
		accepted = accepted[n:]
	}
	if batch.Closed {
		a.closeLogStream(st, "source_closed")
		delete(a.sparkLogs.streams, st.id)
	}
	a.writeJSON(w, 200, map[string]any{"accepted": true, "last_sequence": st.lastSequence})
}
