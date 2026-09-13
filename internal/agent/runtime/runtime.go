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
	CfgPath string
	State   *configfile.State
	Cred    string
	Clock   clock.Clock
	Host    *collectors.Host
	Ping    *collectors.Pinger
	Spool   *spool.Store
	Session string
	Seq     atomic.Int64
	cfg     protocol.AgentConfig
	cfgRev  int64
	cfgHash string
	mu      sync.Mutex
	client  *http.Client
	jobs    map[string]protocol.JobReceipt
	started time.Time
	selfBin string
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
			Timeout: 20 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
				Proxy:           nil,
			},
		},
		jobs: map[string]protocol.JobReceipt{}, started: time.Now(), selfBin: self,
	}
	a.cfgHash = configfile.HashConfig(cfg)
	a.cfgRev = st.File.AppliedRevision
	return a, nil
}

func (a *Agent) Run(ctx context.Context) error {
	t := a.Clock.NewTicker(protocol.ReportInterval)
	defer t.Stop()
	discEvery := time.Duration(a.cfg.Intervals.DiscoverySeconds) * time.Second
	if discEvery <= 0 {
		discEvery = protocol.DiscoveryInterval
	}
	lastDisc := time.Time{}
	a.tick(ctx, true)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C():
			doDisc := lastDisc.IsZero() || a.Clock.Now().Sub(lastDisc) >= discEvery
			a.tick(ctx, doDisc)
			if doDisc {
				lastDisc = a.Clock.Now()
			}
		}
	}
}

func (a *Agent) tick(ctx context.Context, discover bool) {
	now := a.Clock.Now().UTC()
	host, caps := a.Host.Snapshot(now)
	if a.cfg.Ping.Enabled {
		tgt := a.cfg.Ping.Target
		if tgt == "" {
			tgt = "8.8.8.8"
		}
		pctx, cancel := context.WithTimeout(ctx, protocol.PingTimeout)
		a.Ping.Observe(pctx, tgt, protocol.PingTimeout, now)
		cancel()
		sum, pcap := a.Ping.Summary(now, protocol.PingWindow, tgt)
		host.Ping = sum
		caps["ping"] = pcap
	}
	locals, _ := netutil.LocalInterfaceIPs()
	var checkObs []protocol.CheckObservation
	if !a.cfg.Paused {
		for _, def := range a.cfg.Checks {
			cctx, cancel := context.WithTimeout(ctx, protocol.HTTPProbeTimeout+time.Second)
			obs := checks.Run(cctx, def, locals, "", "")
			obs.ConfigRev = a.cfgRev
			checkObs = append(checkObs, obs)
			cancel()
		}
	}
	var disc *protocol.DiscoveryDelta
	if discover && !a.cfg.Paused {
		ls, err := discovery.Listeners()
		if err == nil {
			targets := discovery.DialTargets(ls, locals)
			disc = discovery.Identify(targets, protocol.DiscoveryBudgetMin, 800*time.Millisecond)
			for i := range disc.Confirmed {
				for _, l := range ls {
					if netutil.FormatDial(l.IP, fmt.Sprintf("%d", l.Port)) == disc.Confirmed[i].DialTarget {
						disc.Confirmed[i].ProcessName = l.Process
						disc.Confirmed[i].PID = l.PID
					}
				}
			}
		} else {
			caps["discovery"] = protocol.Capability{Status: protocol.CapError, Reason: err.Error()}
		}
	}
	digest := fileDigest(a.selfBin)
	rep := protocol.AgentReport{
		SchemaVersion: protocol.SchemaVersion, AgentID: a.State.File.AgentID, SessionID: a.Session,
		Sequence: a.Seq.Add(1), ObservedAt: now, ReportedAt: now,
		ConfigRevision: a.cfgRev, ConfigHash: a.cfgHash,
		EndpointGeneration: a.State.File.EndpointGeneration,
		WorkerVersion: version.Version, WorkerDigest: digest,
		Capabilities: caps, Host: host, Checks: checkObs, Discovery: disc,
		IsLive: true, Spool: ptrSpool(a.Spool.Status()),
	}
	a.mu.Lock()
	for _, r := range a.jobs {
		rep.JobReceipts = append(rep.JobReceipts, r)
	}
	a.mu.Unlock()
	if err := a.send(ctx, rep); err != nil {
		rep.IsLive = true
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
	if err := json.NewDecoder(resp.Body).Decode(&cr); err != nil {
		return err
	}
	a.applyControl(cr)
	return nil
}

func (a *Agent) applyControl(cr protocol.ControlResponse) {
	if cr.DesiredConfig != nil {
		body := cr.DesiredConfig.Body
		hash := configfile.HashConfig(body)
		_ = configfile.SaveAppliedConfig(a.State.File.StateDir, body, cr.DesiredConfig.Revision, hash)
		a.cfg = body
		a.cfgRev = cr.DesiredConfig.Revision
		a.cfgHash = hash
		a.State.File.AppliedRevision = a.cfgRev
		a.State.File.AppliedHash = hash
		_ = a.State.Save()
	}
	if cr.Migration != nil {
		_ = configfile.SaveMigration(a.State.File.StateDir, cr.Migration)
		if cr.Migration.Mode == "activate" && cr.Migration.CandidateURL != "" {
			a.State.File.ControllerURL = cr.Migration.CandidateURL
			if cr.Migration.TrustPEM != "" {
				a.State.File.CACertPEM = cr.Migration.TrustPEM
			}
			a.State.File.EndpointGeneration = cr.Migration.Generation
			_ = a.State.Save()
		}
	}
	for _, job := range cr.Jobs {
		a.handleJob(job)
	}
}

func (a *Agent) handleJob(job protocol.JobEnvelope) {
	rec := protocol.JobReceipt{JobID: job.JobID, OperationID: job.OperationID, Status: protocol.TargetSucceeded, Stage: "applied", Message: job.Action}
	now := time.Now().UTC()
	rec.AcceptedAt = &now
	switch job.Action {
	case "agent.collect_now", "agent.discover_now":
		rec.Stage = "fresh_job_result"
		rec.Message = "collection scheduled"
	case "agent.diagnostics":
		rec.Stage = "bounded_redacted_receipt"
		rec.Evidence = map[string]any{"os": runtime.GOOS, "arch": runtime.GOARCH, "version": version.Version, "go": runtime.Version()}
	case "agent.restart":
		if !a.State.File.Managed {
			rec.Status = protocol.TargetUnsupported
			rec.Message = "restart requires managed service host"
			rec.ErrorCode = "unmanaged"
		} else {
			rec.Stage = "restart_requested"
			rec.Message = "service host will restart worker"
		}
	case "update.rollout", "update.rollback":
		if !a.State.File.Managed {
			rec.Status = protocol.TargetUnsupported
			rec.Message = "binary update requires managed service host"
			rec.ErrorCode = "unmanaged"
		} else {
			rec.Status = protocol.TargetAccepted
			rec.Stage = "update.preflight"
			rec.Message = "update accepted; service host performs activation"
			rec.Evidence = map[string]any{"worker_digest": fileDigest(a.selfBin)}
		}
	case "rebind.prepare":
		rec.Stage = "rebind.prepared"
		rec.Message = "plan persisted"
	case "rebind.arm":
		rec.Stage = "rebind.armed"
		rec.Message = "fallback armed"
	case "rebind.activate":
		rec.Stage = "rebind.activating"
		rec.Message = "candidate endpoint stored"
	default:
		rec.Stage = "applied"
	}
	applied := time.Now().UTC()
	rec.AppliedAt = &applied
	a.mu.Lock()
	a.jobs[job.JobID] = rec
	a.mu.Unlock()
}

func (a *Agent) drain(ctx context.Context) {
	items, _ := a.Spool.List()
	for _, it := range items {
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
