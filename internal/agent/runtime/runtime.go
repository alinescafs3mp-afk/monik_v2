package runtime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/agent/collectors"
	"github.com/alinescafs3mp-afk/monik_v2/internal/agent/configfile"
	"github.com/alinescafs3mp-afk/monik_v2/internal/agent/discovery"
	"github.com/alinescafs3mp-afk/monik_v2/internal/agent/spool"
	"github.com/alinescafs3mp-afk/monik_v2/internal/clock"
	"github.com/alinescafs3mp-afk/monik_v2/internal/idgen"
	"github.com/alinescafs3mp-afk/monik_v2/internal/jsonutil"
	"github.com/alinescafs3mp-afk/monik_v2/internal/netutil"
	"github.com/alinescafs3mp-afk/monik_v2/internal/processlock"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"github.com/alinescafs3mp-afk/monik_v2/internal/secure"
	"github.com/alinescafs3mp-afk/monik_v2/internal/tlsutil"
	"github.com/alinescafs3mp-afk/monik_v2/internal/version"
)

type Agent struct {
	CfgPath            string
	State              *configfile.State
	Cred               string
	Clock              clock.Clock
	Host               *collectors.Host
	Ping               *collectors.Pinger
	Spool              *spool.Store
	Session            string
	Seq                atomic.Int64
	cfg                protocol.AgentConfig
	cfgRev             int64
	cfgHash            string
	mu                 sync.Mutex
	client             *http.Client
	receiptCursor      string
	jobs               map[string]protocol.JobReceipt
	started            time.Time
	selfBin            string
	digest             string
	discoveries        chan *protocol.DiscoveryDelta
	observations       chan protocol.CheckObservation
	discovering        atomic.Bool
	scheduler          checkScheduler
	advisor            discovery.Advisor
	trialRunning       atomic.Int32
	workerTasks        sync.WaitGroup
	runCtx             context.Context
	stopping           bool
	forceDiscovery     bool
	secrets            map[string]storedSecret
	lastContact        time.Time
	nextMigrationTrial time.Time
}

func Open(cfgPath string) (*Agent, error) {
	st, err := configfile.Load(cfgPath)
	if err != nil {
		return nil, err
	}
	if st.File.AgentID == "" || st.File.ControllerID == "" || st.File.StateDir == "" || st.File.SchemaVersion != protocol.SchemaVersion {
		return nil, fmt.Errorf("incomplete enrolled agent configuration")
	}
	if _, err := netutil.ValidateControllerURL(st.File.ControllerURL); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(st.File.StateDir, 0700); err != nil {
		return nil, err
	}
	unlock, err := processlock.Acquire(filepath.Join(st.File.StateDir, "worker.lock"))
	if err != nil {
		return nil, err
	}
	defer unlock()
	// Import the previous trust-bundle format exactly once. New changes live in
	// the atomically replaced agent configuration, including after a rebind.
	if raw, readErr := os.ReadFile(trustBundlePath(st.File.StateDir)); readErr == nil {
		if in, e := loadIntent(st.File.StateDir); e != nil || in.Kind != "rebind_switch" || st.File.ControllerURL != in.CandidateURL {
			if _, e := tlsutil.PoolFromPEM(raw); e != nil {
				return nil, e
			}
			st.File.CACertPEM = string(raw)
			if e := st.Save(); e != nil {
				return nil, e
			}
		}
		if e := os.Remove(trustBundlePath(st.File.StateDir)); e != nil {
			return nil, e
		}
	}
	cred, err := configfile.ReadCredential(st.File.CredentialPath)
	if err != nil {
		return nil, err
	}
	sp, err := spool.Open(filepath.Join(st.File.StateDir, "spool"))
	if err != nil {
		return nil, err
	}
	cfg := protocol.DefaultAgentConfig()
	revision := int64(0)
	if applied, e := configfile.LoadAppliedEnvelope(st.File.StateDir); e == nil {
		cfg = applied.Body
		revision = applied.Revision
		if revision < 0 {
			revision = st.File.AppliedRevision
		}
		// A previous worker can apply a newer configuration while rolled back.
		// Adopt it only with its matching persisted revision/hash, never by mtime.
		if st.File.AppliedRevision > revision {
			raw, e := os.ReadFile(filepath.Join(st.File.StateDir, "applied.json"))
			var legacy protocol.AgentConfig
			if e == nil && json.Unmarshal(raw, &legacy) == nil && protocol.ValidateAgentConfig(legacy) == nil && configfile.HashConfig(legacy) == st.File.AppliedHash {
				cfg = legacy
				revision = st.File.AppliedRevision
				if e := configfile.SaveAppliedConfig(st.File.StateDir, cfg, revision, st.File.AppliedHash); e != nil {
					return nil, e
				}
			} else {
				return nil, fmt.Errorf("applied configuration is older than its identity mirror; restore the matching protected state")
			}
		}
		if st.File.AppliedHash != "" && st.File.AppliedRevision == revision && configfile.HashConfig(cfg) != st.File.AppliedHash {
			return nil, fmt.Errorf("applied configuration disagrees with its identity mirror")
		}
	} else if !os.IsNotExist(e) {
		return nil, fmt.Errorf("applied config journal: %w", e)
	} else if st.File.AppliedHash != "" {
		return nil, fmt.Errorf("applied configuration is missing for an initialized worker; restore protected state, do not reset defaults")
	}

	pool, err := tlsutil.PoolFromPEM([]byte(st.File.CACertPEM))
	if err != nil {
		return nil, err
	}
	self, _ := os.Executable()
	a := &Agent{
		CfgPath: cfgPath, State: st, Cred: cred, Clock: clock.Real{},
		Host: collectors.NewHost(), Ping: collectors.NewPinger(), Spool: sp,
		Session: idgen.New(), cfg: cfg, client: &http.Client{
			Timeout:       4 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
				Proxy:           nil,
			},
		},
		jobs: map[string]protocol.JobReceipt{}, started: time.Now(), selfBin: self, digest: fileDigest(self), discoveries: make(chan *protocol.DiscoveryDelta, 1), observations: make(chan protocol.CheckObservation, 128),
		secrets: map[string]storedSecret{},
	}
	a.lastContact = a.Clock.Now()
	a.cfgHash = configfile.HashConfig(cfg)
	a.cfgRev = revision
	if err := a.applyTrustPool(); err != nil {
		return nil, fmt.Errorf("controller trust: %w", err)
	}
	if err := a.loadJobs(); err != nil {
		return nil, err
	}
	if err := a.restoreIntentReceipt(); err != nil {
		return nil, err
	}
	a.reconcileIntents()
	return a, nil
}

func (a *Agent) Run(ctx context.Context) error {
	unlock, err := processlock.Acquire(filepath.Join(a.State.File.StateDir, "worker.lock"))
	if err != nil {
		return err
	}
	defer unlock()
	runCtx, cancel := context.WithCancel(ctx)
	a.mu.Lock()
	a.runCtx = runCtx
	a.stopping = false
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		a.stopping = true
		a.mu.Unlock()
		cancel()
		a.workerTasks.Wait()
		a.mu.Lock()
		a.client.CloseIdleConnections()
		a.mu.Unlock()
	}()
	ctx = runCtx
	t := a.Clock.NewTicker(protocol.ReportInterval)
	defer t.Stop()
	discEvery := time.Duration(a.cfg.Intervals.DiscoverySeconds) * time.Second
	if discEvery <= 0 {
		discEvery = protocol.DiscoveryInterval
	}
	lastDisc := a.Clock.Now()
	a.tick(ctx, true)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C():
			discEvery = time.Duration(a.cfg.Intervals.DiscoverySeconds) * time.Second
			if discEvery < 5*time.Second {
				discEvery = protocol.DiscoveryInterval
			}
			doDisc := a.forceDiscovery || a.Clock.Now().Sub(lastDisc) >= discEvery
			a.tick(ctx, doDisc)
			if doDisc {
				lastDisc = a.Clock.Now()
			}
		}
	}
}

func (a *Agent) tick(ctx context.Context, discover bool) {
	if ctx.Err() != nil {
		return
	}
	now := a.Clock.Now().UTC()
	a.mu.Lock()
	a.reconcileIntents()
	a.maintainMigration()
	a.mu.Unlock()
	var host *protocol.HostMetrics
	caps := map[string]protocol.Capability{}
	collectRequested := false
	a.mu.Lock()
	for _, job := range a.jobs {
		if job.Status == protocol.TargetAccepted && job.Stage == "collect_pending" {
			collectRequested = true
		}
	}
	a.mu.Unlock()
	if !a.cfg.Paused || collectRequested {
		host, caps = a.Host.Snapshot(now)
	}
	if host != nil && a.cfg.Ping.Enabled {
		pctx, cancel := context.WithTimeout(ctx, protocol.PingTimeout)
		a.Ping.Observe(pctx, a.cfg.Ping.Target, protocol.PingTimeout, now)
		cancel()
		sum, cap := a.Ping.Summary(now, protocol.PingWindow, a.cfg.Ping.Target)
		host.Ping = sum
		caps["ping"] = cap
	} else if host != nil {
		host.Ping = &protocol.PingSummary{Target: a.cfg.Ping.Target, WindowSec: int(protocol.PingWindow.Seconds()), Status: "disabled", Reason: "Ping выключен в конфигурации"}
		caps["ping"] = protocol.Capability{Status: protocol.CapPartial, Reason: "Ping выключен в конфигурации"}
	}
	locals, _ := netutil.LocalInterfaceIPs()
	if !a.cfg.Paused {
		a.scheduleChecks(ctx, now, locals)
	}
	caps["http_custom_v1"] = protocol.Capability{Status: "supported", Reason: "bounded custom requests, typed assertions and per-check intervals"}
	caps["immutable_release_v1"] = protocol.Capability{Status: "supported", Reason: "release-specific metadata and authenticated target URLs"}
	caps["selective_monitor_v1"] = protocol.Capability{Status: "supported", Reason: "paused checks excluded from scheduler and rediscovery probes"}
	caps["listener_inventory_v1"] = protocol.Capability{Status: "supported", Reason: "separate complete OS listener snapshot and bounded protocol identification"}
	caps["health_advisor_v1"] = protocol.Capability{Status: "supported", Reason: "bounded local suggestions; no auth/TLS bypass or custom-check replacement"}
	if discover && !a.cfg.Paused && a.discovering.CompareAndSwap(false, true) {
		a.forceDiscovery = false
		disabled := discovery.DisabledTargets(a.cfg.Checks, locals)
		for _, t := range a.cfg.DiscoveryDisabledTargets {
			disabled[t] = protocol.CheckDefinition{}
		}
		advise := a.cfg.AutoMonitorNew
		a.workerTasks.Add(1)
		go func() {
			defer a.workerTasks.Done()
			defer a.discovering.Store(false)
			ls, err := discovery.Listeners()
			var d *protocol.DiscoveryDelta
			if err != nil {
				d = &protocol.DiscoveryDelta{StartedAt: time.Now().UTC(), EndedAt: time.Now().UTC(), PermissionGaps: []string{err.Error()}}
			} else {
				d = discovery.InspectListenersPolicy(ctx, ls, locals, protocol.DiscoveryBudgetMin, &a.advisor, disabled, advise)
			}
			select {
			case a.discoveries <- d:
			case <-ctx.Done():
			}
		}()
	}
	var disc *protocol.DiscoveryDelta
	select {
	case disc = <-a.discoveries:
	default:
	}
	obs := make([]protocol.CheckObservation, 0)
	for len(obs) < 128 {
		select {
		case o := <-a.observations:
			obs = append(obs, o)
		default:
			goto drained
		}
	}
drained:
	seq := a.Seq.Add(1)
	rep := protocol.AgentReport{SchemaVersion: protocol.SchemaVersion, AgentID: a.State.File.AgentID, SessionID: a.Session, Sequence: seq, ObservedAt: now, ReportedAt: a.Clock.Now().UTC(), ConfigRevision: a.cfgRev, ConfigHash: a.cfgHash, EndpointGeneration: a.State.File.EndpointGeneration, WorkerVersion: version.Version, WorkerDigest: a.digest, ManagedReady: a.managed(), Host: host, Capabilities: caps, Checks: obs, Discovery: disc, IsLive: true, Spool: ptrSpool(a.Spool.Status())}
	a.mu.Lock()
	rep.Migration = a.migrationStatus()
	for id, job := range a.jobs {
		if job.Status == protocol.TargetAccepted {
			wasDiscovery := job.Stage == "discover_pending"
			completed := job.Stage == "collect_pending" && host != nil
			if job.Stage == "discover_pending" && disc != nil && job.AcceptedAt != nil && !disc.StartedAt.Before(*job.AcceptedAt) {
				completed = true
			}
			if completed {
				job.Status = protocol.TargetSucceeded
				job.Stage = "fresh_result"
				job.Message = "fresh observation committed with this receipt"
				job.AppliedAt = &now
				job.Evidence = map[string]any{"sequence": seq, "session_id": a.Session, "observed_at": now}
				if wasDiscovery && disc != nil && len(disc.PermissionGaps) > 0 {
					job.Status = protocol.TargetFailed
					job.Message = "discovery failed"
				}
				a.jobs[id] = job
			}
		}
	}
	rep.JobReceipts = a.receiptBatchLocked(64)
	if err := a.saveJobsLocked(); err != nil {
		rep.JobReceipts = nil
	}
	a.mu.Unlock()
	if err := a.send(ctx, rep); err != nil {
		_ = a.Spool.Push(rep)
		return
	}
	a.drain(ctx)
}

func ptrSpool(s protocol.SpoolStatus) *protocol.SpoolStatus { return &s }

func fileDigest(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func (a *Agent) send(ctx context.Context, rep protocol.AgentReport) error {
	b, err := json.Marshal(rep)
	if err != nil {
		return err
	}
	a.mu.Lock()
	sentURL, controllerID, agentID, cred, client := a.State.File.ControllerURL, a.State.File.ControllerID, a.State.File.AgentID, a.Cred, a.client
	a.mu.Unlock()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(sentURL, "/")+"/api/v1/agent/report", bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cred)
	req.Header.Set("X-Monik-Agent-Id", agentID)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		slurp, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("report status %d %s", resp.StatusCode, slurp)
	}
	var cr protocol.ControlResponse
	if err := jsonutil.ReadObject(resp.Body, 4<<20, &cr); err != nil {
		return err
	}
	if cr.ControllerID != controllerID || cr.Ack == nil || !cr.Ack.Committed || cr.Ack.UpToSequence != rep.Sequence {
		return fmt.Errorf("invalid controller identity or uncommitted acknowledgement")
	}
	if rep.IsLive {
		a.mu.Lock()
		a.lastContact = a.Clock.Now()
		a.recordMigrationContact(sentURL)
		a.mu.Unlock()
		a.applyControl(cr, rep.JobReceipts)
	}
	return nil
}

func (a *Agent) applyControl(cr protocol.ControlResponse, sentReceipts ...[]protocol.JobReceipt) {
	a.mu.Lock()
	// A revision identifies immutable content. Replaying it cannot replace the
	// accepted configuration even when another body has a valid self-hash.
	if cr.DesiredConfig != nil && cr.DesiredConfig.Revision > a.cfgRev {
		d := cr.DesiredConfig
		hash := configfile.HashConfig(d.Body)
		if hash == d.Hash && protocol.ValidateAgentConfig(d.Body) == nil {
			if err := configfile.SaveAppliedConfig(a.State.File.StateDir, d.Body, d.Revision, hash); err == nil {
				a.State.File.AppliedRevision = d.Revision
				a.State.File.AppliedHash = hash
				// The atomic applied envelope, not the identity-file mirror, is authoritative.
				a.cfg = d.Body
				a.cfgRev = d.Revision
				a.cfgHash = hash
				_ = a.State.Save()
			}
		}
	}
	// Receipt delivery never authorizes a URL switch. Candidate trials are managed
	// separately and confirmed only by committed live exchanges on that URL.
	sent := map[string]protocol.JobReceipt{}
	if len(sentReceipts) == 1 {
		for _, rec := range sentReceipts[0] {
			sent[rec.JobID] = rec
		}
	}
	// Keep the durable replay guard until the replacement journal succeeds.
	removed := map[string]protocol.JobReceipt{}
	for _, id := range cr.ReceiptAcks {
		job, ok := a.jobs[id]
		if !ok {
			continue
		}
		transmitted, wasSent := sent[id]
		if !wasSent || !sameReceipt(job, transmitted) {
			continue
		}
		switch transmitted.Status {
		case protocol.TargetSucceeded, protocol.TargetFailed, protocol.TargetRejected, protocol.TargetUnsupported, protocol.TargetExpired, protocol.TargetRolledBack, protocol.TargetUnknownResult:
			removed[id] = job
			delete(a.jobs, id)
		}
		// An ACK for acceptance is not an ACK for completion. Keep pending
		// work durable until its actual result has been sent and acknowledged.
	}
	if err := a.saveJobsLocked(); err != nil {
		for id, rec := range removed {
			a.jobs[id] = rec
		}
	}
	a.mu.Unlock()
	for _, job := range cr.Jobs {
		a.handleJob(job)
	}
}

func (a *Agent) handleJob(job protocol.JobEnvelope) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.stopping {
		return
	}
	if _, exists := a.jobs[job.JobID]; exists {
		return
	}
	if len(a.jobs) >= 1000 {
		return
	}
	now := a.Clock.Now().UTC()
	rec := protocol.JobReceipt{JobID: job.JobID, OperationID: job.OperationID, Status: protocol.TargetUnsupported, Stage: "unsupported", Message: "action is not implemented by this worker", ErrorCode: "not_implemented", AcceptedAt: &now}
	if job.SchemaVersion != protocol.SchemaVersion || job.ControllerID != a.State.File.ControllerID {
		rec.Status = protocol.TargetRejected
		rec.Message = "controller/schema mismatch"
	} else if !job.Deadline.After(now) || job.NotBefore.After(now) {
		rec.Status = protocol.TargetExpired
		rec.Message = "job is outside its authorized time window"
	} else {
		switch job.Action {
		case "agent.collect_now":
			rec.Status = protocol.TargetAccepted
			rec.Stage = "collect_pending"
			rec.Message = "waiting for a fresh measurement"
			rec.ErrorCode = ""
		case "agent.discover_now":
			if a.cfg.Paused {
				rec.Status = protocol.TargetRejected
				rec.Message = "discovery is paused"
			} else {
				rec.Status = protocol.TargetAccepted
				rec.Stage = "discover_pending"
				rec.Message = "waiting for a new discovery pass"
				rec.ErrorCode = ""
				a.forceDiscovery = true
				a.advisor.Reset()
			}
		case "agent.diagnostics":
			rec.Status = protocol.TargetSucceeded
			rec.Stage = "diagnostics"
			rec.ErrorCode = ""
			rec.Message = "redacted diagnostics collected"
			rec.Evidence = map[string]any{"os": runtime.GOOS, "arch": runtime.GOARCH, "version": version.Version, "go": runtime.Version()}
		case "check.trial":
			if a.startTrialLocked(job, rec) {
				return
			}
			rec.Status = protocol.TargetRejected
			rec.Message = "trial concurrency limit reached; retry explicitly"
			rec.ErrorCode = "busy"
		case "profile.apply", "check.apply", "service.pause", "service.ignore":
			expected, _ := job.Params["_expected_config_hash"].(string)
			if job.ExpectedRevision != nil && *job.ExpectedRevision == a.cfgRev && expected == a.cfgHash {
				rec.Status = protocol.TargetSucceeded
				rec.Stage = "applied_revision_hash"
				rec.Message = "configuration persisted and applied"
				rec.ErrorCode = ""
				rec.Evidence = map[string]any{"revision": a.cfgRev, "hash": a.cfgHash}
			} else {
				rec.Status = protocol.TargetRejected
				rec.Message = "desired revision/hash was not applied or has been superseded"
			}
		case "secret.replace", "agent.restart", "update.rollout", "update.rollback",
			"rebind.prepare", "rebind.arm", "rebind.activate", "rebind.retire",
			"credential.rotate", "trust.stage", "trust.retire":
			rec = a.executeJob(job)
		}
	}
	if rec.Status == protocol.TargetSucceeded {
		rec.AppliedAt = &now
	}
	a.jobs[job.JobID] = rec
	if err := a.saveJobsLocked(); err != nil {
		delete(a.jobs, job.JobID)
	}
}

func (a *Agent) drain(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	items, _ := a.Spool.ListLimit(4)
	for _, it := range items {
		if ctx.Err() != nil {
			return
		}
		it.IsLive = false
		if err := a.send(ctx, it); err != nil {
			return
		}
		if err := a.Spool.DropReport(it); err != nil {
			return
		}
	}
}

func ListenAddrHint() string {
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && !n.IP.IsLoopback() {
			return n.IP.String()
		}
	}
	return "127.0.0.1"
}

func (a *Agent) secretFor(d protocol.CheckDefinition) (header, value string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.secretForLocked(d)
}

func (a *Agent) secretForLocked(d protocol.CheckDefinition) (header, value string) {
	if d.SecretID == "" {
		return "", ""
	}
	s, ok := a.secrets[d.SecretID]
	if !ok {
		hdr, val, ver, err := a.fetchSecret(d.SecretID)
		if err != nil {
			return "", ""
		}
		s = storedSecret{ID: d.SecretID, Header: hdr, Value: val, Version: ver}
		a.secrets[d.SecretID] = s
	}
	return s.Header, s.Value
}

func (a *Agent) saveJobsLocked() error {
	b, err := json.Marshal(a.jobs)
	if err != nil {
		return err
	}
	return secure.AtomicWrite(filepath.Join(a.State.File.StateDir, "job-receipts.json"), b, 0600)
}
