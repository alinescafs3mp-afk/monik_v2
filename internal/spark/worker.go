package spark

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/agent/checks"
	"github.com/alinescafs3mp-afk/monik_v2/internal/agent/configfile"
	"github.com/alinescafs3mp-afk/monik_v2/internal/agent/discovery"
	"github.com/alinescafs3mp-afk/monik_v2/internal/agent/spool"
	"github.com/alinescafs3mp-afk/monik_v2/internal/idgen"
	"github.com/alinescafs3mp-afk/monik_v2/internal/jsonutil"
	"github.com/alinescafs3mp-afk/monik_v2/internal/netutil"
	"github.com/alinescafs3mp-afk/monik_v2/internal/processlock"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"github.com/alinescafs3mp-afk/monik_v2/internal/secure"
	"github.com/alinescafs3mp-afk/monik_v2/internal/tlsutil"
	"github.com/alinescafs3mp-afk/monik_v2/internal/version"
)

type workerJob struct {
	Envelope           protocol.JobEnvelope              `json:"envelope"`
	Request            *ActionRequest                    `json:"request,omitempty"`
	Receipt            protocol.JobReceipt               `json:"receipt"`
	Stage              *protocol.SparkStageAuthorization `json:"stage,omitempty"`
	StageRequest       *ActionRequest                    `json:"stage_request,omitempty"`
	RenewalAttempted   bool                              `json:"renewal_attempted,omitempty"`
	StageSnapshotAcked bool                              `json:"stage_snapshot_acked,omitempty"`
	Acked              bool                              `json:"acked,omitempty"`
	running            bool
	stageRenewing      bool
}
type scheduledCheck struct {
	last    time.Time
	running bool
}
type collectedSnapshot struct {
	host  *protocol.HostMetrics
	caps  map[string]protocol.Capability
	spark *protocol.SparkExtensionV1
	at    time.Time
}
type Worker struct {
	state          *configfile.State
	credential     string
	client         *http.Client
	spool          *spool.Store
	collector      *Collector
	broker         BrokerClient
	session        string
	sequence       int64
	started        time.Time
	unlock         func()
	cfg            protocol.AgentConfig
	revision       int64
	hash           string
	digest         string
	mu             sync.Mutex
	features       map[string]int
	reportGzip     bool
	jobs           map[string]*workerJob
	checks         map[string]scheduledCheck
	observations   chan protocol.CheckObservation
	discovery      chan *protocol.DiscoveryDelta
	discovering    bool
	lastDiscovery  time.Time
	wg             sync.WaitGroup
	ctx            context.Context
	cancel         context.CancelFunc
	brokerSnapshot BrokerSnapshot
	lastBroker     time.Time
	brokerRunning  bool
	logSessions    map[string]context.CancelFunc
	lastContact    time.Time
	collection     collectedSnapshot
	collectRunning bool

	lastBrokerAttempt time.Time
}

func OpenWorker(configPath, brokerSocket string) (*Worker, error) {
	if e := collectorIdentityAllowed(uint32(os.Geteuid()), os.Getpid()); e != nil {
		return nil, e
	}
	state, e := configfile.Load(configPath)
	if e != nil {
		return nil, e
	}
	f := state.File
	if f.Managed && (f.AgentFlavor != "spark-light" || f.RuntimeMode != "direct") {
		return nil, fmt.Errorf("managed config requires explicit spark-light direct conversion")
	}
	if f.AgentFlavor != "" && f.AgentFlavor != "spark-light" {
		return nil, fmt.Errorf("wrong agent flavor")
	}
	if f.SchemaVersion != protocol.SchemaVersion || f.AgentID == "" || f.ControllerID == "" || f.StateDir == "" {
		return nil, fmt.Errorf("enrolled agent configuration required")
	}
	if _, e = netutil.ValidateControllerURL(f.ControllerURL); e != nil {
		return nil, e
	}
	unlock, e := processlock.Acquire(filepath.Join(f.StateDir, "worker.lock"))
	if e != nil {
		return nil, e
	}
	success := false
	defer func() {
		if !success {
			unlock()
		}
	}()
	credential, e := configfile.ReadCredential(f.CredentialPath)
	if e != nil {
		return nil, e
	}
	pool, e := tlsutil.PoolFromPEM([]byte(f.CACertPEM))
	if e != nil {
		return nil, e
	}
	queue, e := spool.Open(filepath.Join(f.StateDir, "spool"))
	if e != nil {
		return nil, e
	}
	cfg := protocol.DefaultAgentConfig()
	revision := int64(0)
	if applied, err := configfile.LoadAppliedEnvelope(f.StateDir); err == nil {
		cfg = applied.Body
		revision = applied.Revision
		if revision < 0 {
			revision = f.AppliedRevision
		}
		if f.AppliedRevision > revision || f.AppliedRevision == revision && f.AppliedHash != "" && f.AppliedHash != configfile.HashConfig(cfg) {
			return nil, fmt.Errorf("applied configuration identity mismatch; preserve protected state")
		}
	} else if !os.IsNotExist(err) || f.AppliedHash != "" {
		return nil, fmt.Errorf("applied configuration missing or invalid")
	}
	w := &Worker{state: state, credential: credential, client: &http.Client{Timeout: 4 * time.Second, Transport: &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}, MaxResponseHeaderBytes: 32 << 10}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, spool: queue, collector: NewCollector(), broker: BrokerClient{Socket: brokerSocket}, session: idgen.New(), started: time.Now(), unlock: unlock, cfg: cfg, revision: revision, hash: configfile.HashConfig(cfg), features: map[string]int{}, jobs: map[string]*workerJob{}, checks: map[string]scheduledCheck{}, observations: make(chan protocol.CheckObservation, 128), discovery: make(chan *protocol.DiscoveryDelta, 1), logSessions: map[string]context.CancelFunc{}}
	if self, err := os.Executable(); err == nil {
		w.digest, _, _ = secure.SHA256File(self)
	}
	if b, err := readPrivateFile(filepath.Join(f.StateDir, "spark-receipts.json"), 4<<20, uint32(os.Geteuid())); err == nil {
		if len(b) > 4<<20 || strictDecode(b, &w.jobs) != nil {
			return nil, fmt.Errorf("Spark receipt journal invalid")
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	success = true
	return w, nil
}
func (w *Worker) Run(ctx context.Context) error {
	w.ctx, w.cancel = context.WithCancel(ctx)
	defer w.cancel()
	defer w.unlock()
	defer w.client.CloseIdleConnections()
	defer w.wg.Wait()
	w.wg.Add(1)
	go func() { defer w.wg.Done(); w.collector.RunGPU(w.ctx) }()
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-w.ctx.Done():
				w.closeLogSessions()
				return
			case <-ticker.C:
				w.mu.Lock()
				supported := w.features["spark_logs_v1"] == 1 && time.Since(w.lastContact) < 15*time.Second
				w.mu.Unlock()
				if supported {
					w.pollLogs()
				} else {
					w.closeLogSessions()
				}
			}
		}
	}()
	if w.broker.Socket != "" {
		probe, end := context.WithTimeout(w.ctx, 2*time.Second)
		_ = w.broker.Bind(probe, w.state.File.AgentID, w.state.File.ControllerID, w.session)
		end()
	}
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	w.tick()
	for {
		select {
		case <-w.ctx.Done():
			return nil
		case <-ticker.C:
			w.tick()
		}
	}
}
func (w *Worker) saveJobsLocked() error {
	if len(w.jobs) > 2048 {
		return fmt.Errorf("Spark receipt capacity")
	}
	b, e := json.Marshal(w.jobs)
	if e != nil {
		return e
	}
	if len(b) > 4<<20 {
		return fmt.Errorf("Spark receipt journal byte limit")
	}
	return secure.AtomicWrite(filepath.Join(w.state.File.StateDir, "spark-receipts.json"), b, 0600)
}
func (w *Worker) tick() {
	if w.ctx.Err() != nil {
		return
	}
	now := time.Now().UTC()
	w.mu.Lock()
	cfg, revision, hash := w.cfg, w.revision, w.hash
	features := map[string]int{}
	for k, v := range w.features {
		features[k] = v
	}
	w.mu.Unlock()
	w.refreshCollection()
	host, caps, spark := w.cachedCollection(now)
	if host != nil {
		host.DisplayName = cfg.DisplayName
	}
	if cfg.Paused {
		host = nil
		spark = nil
	} else {
		w.scheduleChecks(now, cfg, revision)
		w.scheduleDiscovery(now, cfg)
	}
	w.refreshBroker()
	caps["agent_flavor"] = protocol.Capability{Status: protocol.CapSupported, Reason: "spark-light"}
	caps["spark_telemetry_v1"] = protocol.Capability{Status: protocol.CapSupported}
	w.collector.mu.Lock()
	confirmedHardware := w.collector.topology == "unified"
	w.collector.mu.Unlock()
	caps["spark_hardware_v1"] = protocol.Capability{Status: protocol.CapPartial, Reason: "GB10/DGX Spark hardware evidence not yet available"}
	if confirmedHardware {
		caps["spark_hardware_v1"] = protocol.Capability{Status: protocol.CapSupported, Reason: "GB10 or DGX Spark platform observed locally"}
	}
	caps["spark_inventory_v1"] = protocol.Capability{Status: protocol.CapPartial, Reason: "local approved registry; passive process discovery"}
	caps["http_custom_v1"] = protocol.Capability{Status: protocol.CapSupported, Reason: "bounded selected checks preserved"}
	caps["selective_monitor_v1"] = protocol.Capability{Status: protocol.CapSupported}
	caps["listener_inventory_v1"] = protocol.Capability{Status: protocol.CapSupported}
	for _, k := range []string{"agent_console_v1", "immutable_release_v1", "agent.restart", "agent.update", "controller.rebind"} {
		caps[k] = protocol.Capability{Status: protocol.CapUnsupported, Reason: "not implemented by direct spark-light worker"}
	}
	w.mu.Lock()
	snapshot := w.brokerSnapshot
	lastBroker := w.lastBroker
	w.mu.Unlock()
	fresh := !lastBroker.IsZero() && now.Sub(lastBroker) <= protocol.SparkObservationFreshness
	controlCap := protocol.Capability{Status: protocol.CapUnsupported, Reason: "local_broker_not_configured"}
	logsCap := protocol.Capability{Status: protocol.CapUnsupported, Reason: "local_logs_not_approved"}
	if fresh {
		controlCap = protocol.Capability{Status: protocol.CapSupported, Reason: "typed locally approved broker"}
		for _, p := range snapshot.Inventory.Profiles {
			if p.LogAllowed {
				logsCap = protocol.Capability{Status: protocol.CapSupported, Reason: "bounded exact locally approved sources"}
			}
		}
	}
	caps["spark_control_v1"] = controlCap
	caps["spark_logs_v1"] = logsCap
	if spark != nil && features["spark_telemetry_v1"] == 1 {
		if features["spark_inventory_v1"] == 1 && fresh {
			spark.Inventory = &snapshot.Inventory
			spark.ModelStates = snapshot.States
		}
		if features["spark_control_v1"] == 1 {
			if fresh {
				spark.ControlState = &snapshot.Control
			} else {
				spark.ControlState = &protocol.SparkControlState{Enabled: false, Epoch: nil, ObservedAt: now, Reason: "local_broker_unavailable"}
			}
		}
	} else {
		spark = nil
	}
	if features["spark_telemetry_v1"] != 1 {
		caps["spark_extension_negotiated"] = protocol.Capability{Status: protocol.CapPartial, Reason: "controller upgrade required for Spark extension; basic schema-3 host fallback active"}
	}
	observations := []protocol.CheckObservation{}
	for len(observations) < 128 {
		select {
		case o := <-w.observations:
			observations = append(observations, o)
		default:
			goto checksDrained
		}
	}
checksDrained:
	var disc *protocol.DiscoveryDelta
	select {
	case disc = <-w.discovery:
	default:
	}
	w.mu.Lock()
	w.sequence++
	rep := protocol.AgentReport{SchemaVersion: 3, AgentID: w.state.File.AgentID, AgentFlavor: "spark-light", SessionID: w.session, Sequence: w.sequence, ObservedAt: now, ReportedAt: now, EndpointGeneration: w.state.File.EndpointGeneration, ConfigRevision: revision, ConfigHash: hash, WorkerVersion: version.Version, WorkerDigest: w.digest, ManagedReady: w.state.File.Managed && w.state.File.AgentFlavor == "spark-light" && w.state.File.RuntimeMode == "direct", Capabilities: caps, Host: host, Spark: spark, Checks: observations, Discovery: disc, IsLive: true}
	status := w.spool.Status()
	rep.Spool = &status
	for _, j := range w.jobs {
		if !j.Acked && len(rep.JobReceipts) < 64 {
			rep.JobReceipts = append(rep.JobReceipts, j.Receipt)
		}
	}
	w.mu.Unlock()
	if e := w.send(w.ctx, rep); e != nil {
		_ = w.spool.Push(rep)
		return
	}
	pending, _ := w.spool.ListLimit(8)
	for _, old := range pending {
		if w.ctx.Err() != nil {
			return
		}
		old.IsLive = false
		old.ReportedAt = time.Now().UTC()
		old.JobReceipts = nil
		if e := w.send(w.ctx, old); e != nil {
			break
		}
		_ = w.spool.DropReport(old)
	}
	if fresh {
		w.reconcilePending()
	}
}
func (w *Worker) refreshCollection() {
	w.mu.Lock()
	if w.collectRunning || w.ctx.Err() != nil {
		w.mu.Unlock()
		return
	}
	w.collectRunning = true
	w.mu.Unlock()
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		at := time.Now().UTC()
		host, caps, extension := w.collector.Snapshot(at)
		w.mu.Lock()
		w.collection = collectedSnapshot{host: host, caps: caps, spark: extension, at: at}
		w.collectRunning = false
		w.mu.Unlock()
	}()
}
func (w *Worker) cachedCollection(now time.Time) (*protocol.HostMetrics, map[string]protocol.Capability, *protocol.SparkExtensionV1) {
	w.mu.Lock()
	cached := w.collection
	w.mu.Unlock()
	caps := map[string]protocol.Capability{}
	for k, v := range cached.caps {
		caps[k] = v
	}
	var host *protocol.HostMetrics
	if cached.host != nil {
		copy := *cached.host
		host = &copy
	}
	var extension *protocol.SparkExtensionV1
	if cached.spark != nil {
		copy := *cached.spark
		copy.Metrics = append([]protocol.SparkMetric{}, cached.spark.Metrics...)
		extension = &copy
	}
	if cached.at.IsZero() || now.Sub(cached.at) > 15*time.Second {
		host = nil
		caps["host_collection"] = protocol.Capability{Status: protocol.CapError, Reason: "collector_pending_or_stalled; one outstanding probe; no overlapping replacements"}
		if extension != nil {
			for i := range extension.Metrics {
				o := &extension.Metrics[i].Observation
				if o.Value != nil {
					o.Quality = "stale"
					o.Reason = "host_collector_stale"
				}
			}
		}
	} else {
		caps["host_collection"] = protocol.Capability{Status: protocol.CapSupported}
	}
	return host, caps, extension
}
func (w *Worker) refreshBroker() {
	if w.broker.Socket == "" {
		return
	}
	w.mu.Lock()
	now := time.Now()
	if !w.brokerRefreshDueLocked(now) {
		w.mu.Unlock()
		return
	}
	w.brokerRunning = true
	// Failed probes consume the same background interval as successful ones.
	// Record the attempt before launching, while the no-overlap guard is held.
	w.lastBrokerAttempt = now
	w.mu.Unlock()
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		defer func() { w.mu.Lock(); w.brokerRunning = false; w.mu.Unlock() }()
		probe, end := context.WithTimeout(w.ctx, 8*time.Second)
		defer end()
		if e := w.broker.Bind(probe, w.state.File.AgentID, w.state.File.ControllerID, w.session); e != nil {
			return
		}
		snapshot, e := w.broker.Snapshot(probe)
		if e != nil {
			return
		}
		w.mu.Lock()
		w.brokerSnapshot = snapshot
		w.lastBroker = time.Now()
		w.mu.Unlock()
	}()
}

// The report/contact loop stays at five seconds. Only idle model observation is
// slower; actions and transitions retain its normal cadence. Reading logs does not
// increase model health polling.
// The caller holds w.mu. Cached observations keep their original timestamps.
func (w *Worker) brokerRefreshDueLocked(now time.Time) bool {
	if w.brokerRunning {
		return false
	}
	if w.lastBrokerAttempt.IsZero() || now.Sub(w.lastBrokerAttempt) >= 30*time.Second {
		return true
	}
	for _, job := range w.jobs {
		if !job.Acked || job.running || job.stageRenewing || member(string(job.Receipt.Status), string(protocol.TargetAccepted), string(protocol.TargetRunning), string(protocol.TargetUnknownResult), string(protocol.TargetAwaitingConfirmation)) {
			return true
		}
	}
	for _, state := range w.brokerSnapshot.States {
		if member(state.SupervisorState, "starting", "stopping") {
			return true
		}
	}
	return false
}
func (w *Worker) authenticatedRequest(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var data []byte
	var e error
	if body != nil {
		data, e = json.Marshal(body)
		if e != nil {
			return nil, e
		}
	}
	encoding := ""
	if method == http.MethodPost && path == "/api/v1/agent/report" {
		w.mu.Lock()
		supported := w.reportGzip
		w.mu.Unlock()
		data, encoding, e = encodeReportBody(data, supported)
		if e != nil {
			return nil, e
		}
	}
	req, e := http.NewRequestWithContext(ctx, method, strings.TrimRight(w.state.File.ControllerURL, "/")+path, bytes.NewReader(data))
	if e != nil {
		return nil, e
	}
	req.Header.Set("Content-Type", "application/json")
	if encoding != "" {
		req.Header.Set("Content-Encoding", encoding)
	}
	req.Header.Set("Authorization", "Bearer "+w.credential)
	req.Header.Set("X-Monik-Agent-Id", w.state.File.AgentID)
	response, e := w.client.Do(req)
	if encoding != "" && response != nil && response.StatusCode != http.StatusOK {
		// A controller rollback may no longer accept gzip. Preserve the failed
		// envelope for the normal spool retry; do not issue an immediate resend.
		w.mu.Lock()
		w.reportGzip = false
		w.mu.Unlock()
	}
	return response, e
}
func (w *Worker) send(ctx context.Context, rep protocol.AgentReport) error {
	requestStarted := time.Now()
	response, e := w.authenticatedRequest(ctx, "POST", "/api/v1/agent/report", rep)
	if e != nil {
		return fmt.Errorf("controller unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		io.Copy(io.Discard, io.LimitReader(response.Body, 2048))
		return fmt.Errorf("report rejected (%d)", response.StatusCode)
	}
	var cr protocol.ControlResponse
	if e = jsonutil.ReadObject(response.Body, 4<<20, &cr); e != nil {
		return e
	}
	if cr.ControllerID != w.state.File.ControllerID || cr.Ack == nil || !cr.Ack.Committed || cr.Ack.UpToSequence != rep.Sequence {
		return fmt.Errorf("invalid committed controller acknowledgement")
	}
	w.mu.Lock()
	w.reportGzip = reportGzipAdvertised(response.Header)
	w.mu.Unlock()
	if !rep.IsLive {
		return nil
	}
	w.mu.Lock()
	w.lastContact = time.Now()
	w.features = cr.Features
	if cr.DesiredConfig != nil && cr.DesiredConfig.Revision > w.revision {
		d := cr.DesiredConfig
		if d.Hash == configfile.HashConfig(d.Body) && protocol.ValidateAgentConfig(d.Body) == nil {
			if e = configfile.SaveAppliedConfig(w.state.File.StateDir, d.Body, d.Revision, d.Hash); e == nil {
				w.cfg = d.Body
				w.hash = d.Hash
				w.revision = d.Revision
				w.state.File.AppliedRevision = d.Revision
				w.state.File.AppliedHash = d.Hash
				_ = w.state.Save()
			}
		}
	}
	for _, id := range cr.ReceiptAcks {
		if j := w.jobs[id]; j != nil {
			for _, sent := range rep.JobReceipts {
				if sent.JobID == id && digest(sent) == digest(j.Receipt) {
					j.Acked = true
				}
			}
		}
	}
	for _, j := range w.jobs {
		if j.Receipt.Status == protocol.TargetAwaitingConfirmation && j.Receipt.Stage == "restart_stopped" && !j.RenewalAttempted {
			// Receipt acknowledgement and current-state commitment are different
			// facts: asynchronous broker collection can lag the stop receipt.
			j.StageSnapshotAcked = restartStopCommitted(rep, j)
		}
	}
	_ = w.saveJobsLocked()
	w.mu.Unlock()
	for _, j := range cr.Jobs {
		w.acceptJob(j, cr.ServerTime, requestStarted)
	}
	return nil
}

func restartStopCommitted(rep protocol.AgentReport, j *workerJob) bool {
	if !rep.IsLive || rep.Spark == nil || j.Request == nil || rep.Spark.Inventory == nil || rep.Spark.ControlState == nil {
		return false
	}
	p := j.Request.Params
	c := rep.Spark.ControlState
	if rep.SessionID != p.ExpectedAgentSession || !c.Enabled || c.Epoch == nil || *c.Epoch != p.ControlEpoch || j.Receipt.Evidence["profile_id"] != p.ProfileID || j.Receipt.Evidence["profile_revision"] != p.ProfileRevision || j.Receipt.Evidence["control_epoch"] != p.ControlEpoch || j.Receipt.Evidence["agent_session"] != p.ExpectedAgentSession {
		return false
	}
	fresh := func(at time.Time) bool {
		age := rep.ObservedAt.Sub(at)
		return !at.IsZero() && age >= -5*time.Second && age <= protocol.SparkControlFreshness
	}
	if !fresh(c.ObservedAt) {
		return false
	}
	profileMatches := false
	for _, profile := range rep.Spark.Inventory.Profiles {
		if profile.ProfileID == p.ProfileID && profile.ProfileRevision == p.ProfileRevision {
			profileMatches = true
			break
		}
	}
	if !profileMatches {
		return false
	}
	for _, state := range rep.Spark.ModelStates {
		if state.ProfileID != p.ProfileID || state.Quality != "ok" || !fresh(state.ObservedAt) || fmt.Sprint(state.Generation) != fmt.Sprint(j.Receipt.Evidence["generation"]) {
			continue
		}
		if state.SupervisorState == "stopped" {
			return true
		}
		identity, ok := j.Receipt.Evidence["instance_identity"].(string)
		return state.SupervisorState == "failed" && state.Readiness == "not_applicable" && state.Reason == "failed_service_shutdown_verified" && j.Receipt.Evidence["shutdown_verified"] == true && j.Receipt.Evidence["supervisor_state"] == "failed" && j.Receipt.Evidence["readiness"] == "not_applicable" && j.Receipt.Evidence["reason"] == "failed_service_shutdown_verified" && ok && len(identity) == 71 && strings.HasPrefix(identity, "sha256:") && strings.Trim(identity[7:], "0123456789abcdef") == ""
	}
	return false
}

func (w *Worker) acceptJob(job protocol.JobEnvelope, serverTime, requestStarted time.Time) {
	w.mu.Lock()
	if existing := w.jobs[job.JobID]; existing != nil {
		if digest(existing.Envelope) != digest(job) {
			existing.Receipt = rejection(ActionRequest{JobID: job.JobID, OperationID: job.OperationID}, "idempotency_conflict")
			existing.Acked = false
			_ = w.saveJobsLocked()
		}
		w.mu.Unlock()
		return
	}
	if len(w.jobs) >= 2048 {
		w.mu.Unlock()
		return
	}
	r := protocol.JobReceipt{JobID: job.JobID, OperationID: job.OperationID, Status: protocol.TargetUnsupported, Stage: "unsupported", Message: "action unavailable in spark-light"}
	j := &workerJob{Envelope: job, Receipt: r}
	w.jobs[job.JobID] = j
	if job.ControllerID != w.state.File.ControllerID || job.SchemaVersion != 3 {
		j.Receipt.Status = protocol.TargetRejected
		j.Receipt.ErrorCode = "controller_identity_mismatch"
	} else if strings.HasPrefix(job.Action, "model.") && w.features["spark_control_v1"] == 1 {
		params, e := protocol.DecodeModelActionParams(job.Params)
		if e != nil {
			j.Receipt.Status = protocol.TargetRejected
			j.Receipt.ErrorCode = "invalid_model_parameters"
		} else {
			// Include response-body decoding and any wait for the journal mutex.
			// The authenticated server timestamp never begins a new lease at
			// response receipt; only the original budget may authorize an effect.
			issuedBudget := job.Deadline.Sub(serverTime)
			remaining := issuedBudget - time.Since(requestStarted)
			if serverTime.IsZero() || requestStarted.IsZero() || issuedBudget <= 0 || issuedBudget > protocol.SparkDispatchLease || remaining <= 0 {
				j.Receipt.Status = protocol.TargetExpired
				j.Receipt.ErrorCode = "dispatch_expired"
			} else {
				now := time.Now()
				request := ActionRequest{JobID: job.JobID, OperationID: job.OperationID, Action: job.Action, Params: params, IssuedAt: now, Deadline: now.Add(remaining)}
				j.Request = &request
				j.Receipt.Status = protocol.TargetAccepted
				j.Receipt.Stage = "prepared"
				j.Receipt.Message = "durably prepared typed local request"
			}
		}
	}
	if e := w.saveJobsLocked(); e != nil {
		delete(w.jobs, job.JobID)
		w.mu.Unlock()
		return
	}
	execute := j.Request != nil
	w.mu.Unlock()
	if execute {
		w.executeJob(job.JobID, false)
	}
}
func (w *Worker) executeJob(id string, stage bool) {
	w.mu.Lock()
	j := w.jobs[id]
	if j == nil || j.running || j.Request == nil {
		w.mu.Unlock()
		return
	}
	j.running = true
	request := *j.Request
	if stage && j.Stage != nil {
		if j.StageRequest == nil {
			j.running = false
			w.mu.Unlock()
			return
		}
		request = *j.StageRequest
	}
	w.mu.Unlock()
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		ctx, end := context.WithTimeout(w.ctx, time.Hour)
		defer end()
		receipt, e := w.broker.Execute(ctx, request)
		if e != nil {
			receipt = rejection(request, "local_broker_unavailable")
			receipt.Status = protocol.TargetUnknownResult
		}
		if stage {
			receipt.JobID = id
			if receipt.Status == protocol.TargetSucceeded {
				receipt.Stage = "model_restarted"
				if receipt.Evidence == nil {
					receipt.Evidence = map[string]any{}
				}
				receipt.Evidence["stage_id"] = request.JobID
			}
		}
		w.mu.Lock()
		if current := w.jobs[id]; current != nil {
			current.running = false
			current.Receipt = receipt
			current.Acked = false
			_ = w.saveJobsLocked()
		}
		w.mu.Unlock()
		w.refreshBroker()
	}()
}
func (w *Worker) reconcilePending() {
	w.mu.Lock()
	ids := []string{}
	renew := []string{}
	for id, j := range w.jobs {
		if j.Request == nil || j.running {
			continue
		}
		if j.Receipt.Status == protocol.TargetAwaitingConfirmation && j.Receipt.Stage == "restart_stopped" && j.Acked {
			if !j.RenewalAttempted && j.StageSnapshotAcked {
				renew = append(renew, id)
			} else if j.RenewalAttempted && j.StageRequest == nil && !j.stageRenewing {
				// A recovered one-shot renewal without a durably prepared stage
				// cannot have called the broker. Do not renew or replay it.
				w.failUnattemptedStageLocked(j, "stage_authorization_interrupted", "Start stage was never durably prepared; no later start was attempted")
			}
		} else if member(string(j.Receipt.Status), string(protocol.TargetAccepted), string(protocol.TargetRunning), string(protocol.TargetUnknownResult)) {
			ids = append(ids, id)
		}
	}
	w.mu.Unlock()
	for _, id := range ids {
		w.mu.Lock()
		j := w.jobs[id]
		stage := j != nil && j.Stage != nil
		w.mu.Unlock()
		w.executeJob(id, stage)
	}
	for _, id := range renew {
		w.renewStage(id)
	}
}

// Only call this before a stage request has been durably prepared. An existing
// request may have reached the broker and must retain its reconciliation state.
func (w *Worker) failUnattemptedStageLocked(j *workerJob, code, message string) {
	if j.StageRequest != nil || j.Receipt.Status != protocol.TargetAwaitingConfirmation || j.Receipt.Stage != "restart_stopped" {
		return
	}
	stopped := j.Receipt
	stoppedBytes, err := json.Marshal(stopped)
	if err != nil {
		return
	}
	wasAcked := j.Acked
	evidence := map[string]any{"restart_stop_receipt": string(stoppedBytes), "start_stage_attempted": false}
	j.Receipt.Status = protocol.TargetFailed
	j.Receipt.Stage = "restart_start_not_attempted"
	j.Receipt.Message = message
	j.Receipt.ErrorCode = code
	j.Receipt.Retryable = false
	j.Receipt.Evidence = evidence
	j.Acked = false
	if err := w.saveJobsLocked(); err != nil {
		j.Receipt = stopped
		j.Acked = wasAcked
	}
}

func (w *Worker) renewStage(id string) {
	w.mu.Lock()
	j := w.jobs[id]
	if j == nil || j.RenewalAttempted {
		w.mu.Unlock()
		return
	}
	j.RenewalAttempted = true
	if e := w.saveJobsLocked(); e != nil {
		w.mu.Unlock()
		return
	}
	j.stageRenewing = true
	operation := j.Envelope.OperationID
	w.mu.Unlock()
	failureCode := "stage_authorization_unavailable"
	failureMessage := "Start authorization was not received; this restart's start stage was not attempted"
	defer func() {
		w.mu.Lock()
		defer w.mu.Unlock()
		if current := w.jobs[id]; current != nil {
			current.stageRenewing = false
			w.failUnattemptedStageLocked(current, failureCode, failureMessage)
		}
	}()
	started := time.Now()
	ctx, end := context.WithTimeout(w.ctx, 4*time.Second)
	defer end()
	response, e := w.authenticatedRequest(ctx, "POST", "/api/v1/agent/spark/stage", map[string]string{"operation_id": operation, "job_id": id})
	if e != nil {
		return
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return
	}
	var auth protocol.SparkStageAuthorization
	serverTime, timeErr := time.Parse(time.RFC3339Nano, response.Header.Get("X-Monik-Server-Time"))
	if e = jsonutil.ReadObject(response.Body, 64<<10, &auth); e != nil || timeErr != nil || serverTime.Before(auth.IssuedAt) || auth.ParentOperationID != operation || auth.Stage != "start" || !protocol.SparkValidID(auth.StageID) || auth.Params.ExpectedAgentSession != w.session || auth.Deadline.Sub(auth.IssuedAt) <= 0 || auth.Deadline.Sub(auth.IssuedAt) > 30*time.Second {
		failureCode = "invalid_stage_authorization"
		failureMessage = "Start authorization failed validation; this restart's start stage was not attempted"
		return
	}
	w.mu.Lock()
	j = w.jobs[id]
	j.Stage = &auth
	preparedAt := time.Now()
	remaining := auth.Deadline.Sub(serverTime) - preparedAt.Sub(started)
	remaining = min(remaining, auth.Deadline.Sub(preparedAt))
	if remaining <= 0 {
		failureCode = "stage_dispatch_expired"
		failureMessage = "Start authorization expired before preparation; this restart's start stage was not attempted"
		w.mu.Unlock()
		return
	}
	j.StageRequest = &ActionRequest{JobID: auth.StageID, OperationID: operation, Action: "model.start", Params: auth.Params, IssuedAt: preparedAt, Deadline: preparedAt.Add(remaining), ParentJobID: id}
	if e = w.saveJobsLocked(); e != nil {
		j.StageRequest = nil
		failureCode = "stage_journal_unavailable"
		failureMessage = "Start request could not be durably prepared; this restart's start stage was not attempted"
		w.mu.Unlock()
		return
	}
	w.mu.Unlock()
	w.executeJob(id, true)
}
func (w *Worker) scheduleChecks(now time.Time, cfg protocol.AgentConfig, revision int64) {
	w.mu.Lock()
	running := 0
	for _, e := range w.checks {
		if e.running {
			running++
		}
	}
	due := []protocol.CheckDefinition{}
	for _, d := range cfg.Checks {
		e := w.checks[d.ID]
		interval := max(d.IntervalSeconds, 5)
		if !d.Paused && !d.Ignored && !e.running && (e.last.IsZero() || now.Sub(e.last) >= time.Duration(interval)*time.Second) {
			due = append(due, d)
		}
	}
	sort.SliceStable(due, func(i, j int) bool { return w.checks[due[i].ID].last.Before(w.checks[due[j].ID].last) })
	if len(due) > 16-running {
		due = due[:max(0, 16-running)]
	}
	for _, d := range due {
		w.checks[d.ID] = scheduledCheck{last: now, running: true}
	}
	w.mu.Unlock()
	for _, d := range due {
		w.wg.Add(1)
		go func(def protocol.CheckDefinition) {
			defer w.wg.Done()
			defer func() { w.mu.Lock(); e := w.checks[def.ID]; e.running = false; w.checks[def.ID] = e; w.mu.Unlock() }()
			locals, _ := netutil.LocalInterfaceIPs()
			header, value := w.fetchSecret(def.SecretID)
			_, body := w.fetchSecret(def.BodySecretID)
			obs := checks.RunRequest(w.ctx, def, locals, header, value, body)
			obs.ConfigRev = revision
			select {
			case w.observations <- obs:
			case <-w.ctx.Done():
			}
		}(d)
	}
}
func (w *Worker) fetchSecret(id string) (string, string) {
	if id == "" {
		return "", ""
	}
	ctx, end := context.WithTimeout(w.ctx, 2*time.Second)
	defer end()
	r, e := w.authenticatedRequest(ctx, "GET", "/api/v1/agent/secrets/"+url.PathEscape(id), nil)
	if e != nil {
		return "", ""
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return "", ""
	}
	var s struct {
		Header  string `json:"header"`
		Value   string `json:"value"`
		Version int    `json:"version"`
	}
	if jsonutil.ReadObject(r.Body, 128<<10, &s) != nil || len(s.Value) > protocol.MaxRequestBody {
		return "", ""
	}
	return s.Header, s.Value
}
func (w *Worker) scheduleDiscovery(now time.Time, cfg protocol.AgentConfig) {
	w.mu.Lock()
	interval := max(cfg.Intervals.DiscoverySeconds, 60)
	if w.discovering || !w.lastDiscovery.IsZero() && now.Sub(w.lastDiscovery) < time.Duration(interval)*time.Second {
		w.mu.Unlock()
		return
	}
	w.discovering = true
	w.lastDiscovery = now
	w.mu.Unlock()
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		defer func() { w.mu.Lock(); w.discovering = false; w.mu.Unlock() }()
		ctx, end := context.WithTimeout(w.ctx, 10*time.Second)
		defer end()
		listeners, e := discovery.Listeners()
		if e != nil {
			return
		}
		if len(listeners) > 256 {
			listeners = listeners[:256]
		}
		locals, _ := netutil.LocalInterfaceIPs()
		disabled := discovery.DisabledTargets(cfg.Checks, locals)
		for _, target := range cfg.DiscoveryDisabledTargets {
			disabled[target] = protocol.CheckDefinition{}
		}
		result := discovery.InspectListenersPolicy(ctx, listeners, locals, protocol.DiscoveryBudgetMin, nil, disabled, cfg.AutoMonitorNew)
		select {
		case w.discovery <- result:
		case <-w.ctx.Done():
		}
	}()
}
