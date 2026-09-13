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
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/agent/checks"
	"github.com/alinescafs3mp-afk/monik_v2/internal/agent/collectors"
	"github.com/alinescafs3mp-afk/monik_v2/internal/agent/configfile"
	"github.com/alinescafs3mp-afk/monik_v2/internal/agent/discovery"
	"github.com/alinescafs3mp-afk/monik_v2/internal/agent/spool"
	"github.com/alinescafs3mp-afk/monik_v2/internal/clock"
	"github.com/alinescafs3mp-afk/monik_v2/internal/idgen"
	"github.com/alinescafs3mp-afk/monik_v2/internal/netutil"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"github.com/alinescafs3mp-afk/monik_v2/internal/tlsutil"
	"github.com/alinescafs3mp-afk/monik_v2/internal/version"
)

type Agent struct {
	CfgPath        string
	State          *configfile.State
	Cred           string
	Clock          clock.Clock
	Host           *collectors.Host
	Ping           *collectors.Pinger
	Spool          *spool.Store
	Session        string
	Seq            atomic.Int64
	cfg            protocol.AgentConfig
	cfgRev         int64
	cfgHash        string
	mu             sync.Mutex
	client         *http.Client
	jobs           map[string]protocol.JobReceipt
	started        time.Time
	selfBin        string
	digest         string
	discoveries    chan *protocol.DiscoveryDelta
	observations   chan protocol.CheckObservation
	discovering    atomic.Bool
	checking       atomic.Bool
	forceDiscovery bool
}

func Open(cfgPath string) (*Agent, error) {
	st, err := configfile.Load(cfgPath)
	if err != nil {
		return nil, err
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
	if c, err := configfile.LoadAppliedConfig(st.File.StateDir); err == nil {
		cfg = c
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
	}
	a.cfgHash = configfile.HashConfig(cfg)
	a.cfgRev = st.File.AppliedRevision
	if b, err := os.ReadFile(filepath.Join(st.File.StateDir, "job-receipts.json")); err == nil {
		if err := json.Unmarshal(b, &a.jobs); err != nil {
			return nil, fmt.Errorf("job receipt journal corrupt: %w", err)
		}
	}
	return a, nil
}

func (a *Agent) Run(ctx context.Context) error {
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
	now := a.Clock.Now().UTC()
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
	}
	locals, _ := netutil.LocalInterfaceIPs()
	if !a.cfg.Paused && a.checking.CompareAndSwap(false, true) {
		defs := append([]protocol.CheckDefinition(nil), a.cfg.Checks...)
		revision := a.cfgRev
		go func() {
			defer a.checking.Store(false)
			var wg sync.WaitGroup
			sem := make(chan struct{}, 16)
			for _, def := range defs {
				if ctx.Err() != nil {
					break
				}
				select {
				case sem <- struct{}{}:
				case <-ctx.Done():
					return
				}
				wg.Add(1)
				go func(d protocol.CheckDefinition) {
					defer wg.Done()
					defer func() { <-sem }()
					cctx, cancel := context.WithTimeout(ctx, protocol.HTTPProbeTimeout)
					defer cancel()
					obs := checks.Run(cctx, d, locals, "", "")
					obs.ConfigRev = revision
					select {
					case a.observations <- obs:
					case <-ctx.Done():
					}
				}(def)
			}
			wg.Wait()
		}()
	}
	if discover && !a.cfg.Paused && a.discovering.CompareAndSwap(false, true) {
		a.forceDiscovery = false
		go func() {
			defer a.discovering.Store(false)
			ls, err := discovery.Listeners()
			var d *protocol.DiscoveryDelta
			if err != nil {
				d = &protocol.DiscoveryDelta{StartedAt: time.Now().UTC(), EndedAt: time.Now().UTC(), PermissionGaps: []string{err.Error()}}
			} else {
				d = discovery.Identify(discovery.DialTargets(ls, locals), protocol.DiscoveryBudgetMin, 800*time.Millisecond)
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
	rep := protocol.AgentReport{SchemaVersion: protocol.SchemaVersion, AgentID: a.State.File.AgentID, SessionID: a.Session, Sequence: seq, ObservedAt: now, ReportedAt: a.Clock.Now().UTC(), ConfigRevision: a.cfgRev, ConfigHash: a.cfgHash, EndpointGeneration: a.State.File.EndpointGeneration, WorkerVersion: version.Version, WorkerDigest: a.digest, Host: host, Capabilities: caps, Checks: obs, Discovery: disc, IsLive: true, Spool: ptrSpool(a.Spool.Status())}
	a.mu.Lock()
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
	// Keep the journal bounded; no automatic resurrection of expired actions.
	ids := make([]string, 0, len(a.jobs))
	for id := range a.jobs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if len(rep.JobReceipts) >= 64 {
			break
		}
		rep.JobReceipts = append(rep.JobReceipts, a.jobs[id])
	}
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
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.State.File.ControllerURL+"/api/v1/agent/report", bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.Cred)
	req.Header.Set("X-Monik-Agent-Id", a.State.File.AgentID)
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		slurp, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("report status %d %s", resp.StatusCode, slurp)
	}
	var cr protocol.ControlResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&cr); err != nil {
		return err
	}
	if cr.ControllerID != a.State.File.ControllerID || cr.Ack == nil || !cr.Ack.Committed || cr.Ack.UpToSequence != rep.Sequence {
		return fmt.Errorf("invalid controller identity or uncommitted acknowledgement")
	}
	if rep.IsLive {
		a.applyControl(cr)
	}
	return nil
}

func (a *Agent) applyControl(cr protocol.ControlResponse) {
	if cr.DesiredConfig != nil && cr.DesiredConfig.Revision >= a.cfgRev {
		d := cr.DesiredConfig
		hash := configfile.HashConfig(d.Body)
		if hash == d.Hash && protocol.ValidateAgentConfig(d.Body) == nil {
			if err := configfile.SaveAppliedConfig(a.State.File.StateDir, d.Body, d.Revision, hash); err == nil {
				a.State.File.AppliedRevision = d.Revision
				a.State.File.AppliedHash = hash
				if err := a.State.Save(); err == nil {
					a.cfg = d.Body
					a.cfgRev = d.Revision
					a.cfgHash = hash
				}
			}
		}
	}
	// Never replace a known controller URL with an unverified candidate. The
	// migration state machine is explicitly unavailable until its release gate.
	a.mu.Lock()
	for _, id := range cr.ReceiptAcks {
		job, ok := a.jobs[id]
		if !ok {
			continue
		}
		switch job.Status {
		case protocol.TargetSucceeded, protocol.TargetFailed, protocol.TargetRejected, protocol.TargetUnsupported, protocol.TargetExpired, protocol.TargetRolledBack:
			delete(a.jobs, id)
		}
		// An ACK for acceptance is not an ACK for completion. Keep pending
		// work durable until its actual result has been sent and acknowledged.
	}
	_ = a.saveJobsLocked()
	a.mu.Unlock()
	for _, job := range cr.Jobs {
		a.handleJob(job)
	}
}

func (a *Agent) handleJob(job protocol.JobEnvelope) {
	a.mu.Lock()
	defer a.mu.Unlock()
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
			}
		case "agent.diagnostics":
			rec.Status = protocol.TargetSucceeded
			rec.Stage = "diagnostics"
			rec.ErrorCode = ""
			rec.Message = "redacted diagnostics collected"
			rec.Evidence = map[string]any{"os": runtime.GOOS, "arch": runtime.GOARCH, "version": version.Version, "go": runtime.Version()}
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
		a.Spool.Drop(it.ObservedAt)
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

func (a *Agent) saveJobsLocked() error {
	b, err := json.Marshal(a.jobs)
	if err != nil {
		return err
	}
	dir := a.State.File.StateDir
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".job-receipts-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(name, filepath.Join(dir, "job-receipts.json"))
}
