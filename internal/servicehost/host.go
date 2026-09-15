package servicehost

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/idgen"
	"github.com/alinescafs3mp-afk/monik_v2/internal/processlock"
	"github.com/alinescafs3mp-afk/monik_v2/internal/update"
)

// Host supervises the collector worker and performs typed update transactions.
// It is not a general command runner.
type Host struct {
	WorkerBin string
	WorkerCfg string
	StateDir  string
	SockPath  string
	SelfBin   string
	Probation time.Duration
	cmd       *exec.Cmd
	alive     bool
	stopping  bool
	done      chan struct{}
	mu        sync.Mutex
	updateMu  sync.Mutex
}

type Request struct {
	Action string            `json:"action"`
	Params map[string]string `json:"params"`
}

type Response struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
	PID     int    `json:"pid,omitempty"`
	Stage   string `json:"stage,omitempty"`
}

func DefaultSock(stateDir string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(stateDir, "service-host.port")
	}
	return filepath.Join(stateDir, "service-host.sock")
}

func (h *Host) Run(ctx context.Context) error {
	if err := os.MkdirAll(h.StateDir, 0o700); err != nil {
		return err
	}
	unlock, err := processlock.Acquire(filepath.Join(h.StateDir, "service-host.lock"))
	if err != nil {
		return err
	}
	defer unlock()
	if h.SockPath == "" {
		h.SockPath = DefaultSock(h.StateDir)
	}
	if err := h.recoverJournal(); err != nil {
		return err
	}
	ln, err := h.listen()
	if err != nil {
		return err
	}
	runCtx, cancel := context.WithCancel(ctx)
	var workers sync.WaitGroup
	defer func() {
		h.mu.Lock()
		h.stopping = true
		h.mu.Unlock()
		cancel()
		_ = ln.Close()
		workers.Wait()
		h.updateMu.Lock()
		h.stopWorker()
		h.updateMu.Unlock()
	}()
	if err := h.startWorker(); err != nil {
		return err
	}
	workers.Add(3)
	go func() { defer workers.Done(); h.reap(runCtx) }()
	go func() { defer workers.Done(); h.watchControlFile(runCtx) }()
	go func() {
		defer workers.Done()
		<-runCtx.Done()
		h.mu.Lock()
		h.stopping = true
		h.mu.Unlock()
		_ = ln.Close()
	}()
	for {
		c, err := ln.Accept()
		if err != nil {
			if runCtx.Err() != nil {
				return nil
			}
			return err
		}
		workers.Add(1)
		go func() {
			defer workers.Done()
			stop := context.AfterFunc(runCtx, func() { _ = c.Close() })
			defer stop()
			h.handle(c)
		}()
	}
}

func (h *Host) listen() (net.Listener, error) {
	if runtime.GOOS == "windows" {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, err
		}
		_ = os.WriteFile(h.SockPath, []byte(ln.Addr().String()), 0o600)
		return ln, nil
	}
	_ = os.Remove(h.SockPath)
	ln, err := net.Listen("unix", h.SockPath)
	if err != nil {
		return nil, err
	}
	_ = os.Chmod(h.SockPath, 0o600)
	return ln, nil
}

func (h *Host) handle(c net.Conn) {
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(30 * time.Second))
	var req Request
	if err := json.NewDecoder(io.LimitReader(c, 64<<10)).Decode(&req); err != nil {
		return
	}
	_ = json.NewEncoder(c).Encode(h.dispatch(req, true))
}

func (h *Host) dispatch(req Request, fromNetwork bool) Response {
	mutating := req.Action == "restart_worker" || req.Action == "activate_update" || req.Action == "rollback_update"
	if fromNetwork && runtime.GOOS == "windows" && mutating {
		return Response{OK: false, Message: "unauthenticated TCP control cannot authorize this action; use the state-dir control file", Stage: "blocked"}
	}
	switch req.Action {
	case "status":
		h.mu.Lock()
		pid := 0
		if h.cmd != nil && h.cmd.Process != nil {
			pid = h.cmd.Process.Pid
		}
		alive := h.alive
		h.mu.Unlock()
		msg := "stopped"
		if alive {
			msg = "running"
		}
		return Response{OK: true, Message: msg, PID: pid}
	case "restart_worker":
		h.updateMu.Lock()
		defer h.updateMu.Unlock()
		h.mu.Lock()
		stopping := h.stopping
		h.mu.Unlock()
		if stopping {
			return Response{OK: false, Message: "service host is stopping", Stage: "stopped"}
		}
		if err := h.restartWorker(); err != nil {
			return Response{OK: false, Message: err.Error()}
		}
		return Response{OK: true, Message: "restarted"}
	case "activate_update":
		h.updateMu.Lock()
		defer h.updateMu.Unlock()
		h.mu.Lock()
		stopping := h.stopping
		h.mu.Unlock()
		if stopping {
			return Response{OK: false, Message: "service host is stopping", Stage: "stopped"}
		}
		return h.activateUpdate(req.Params)
	case "rollback_update":
		h.updateMu.Lock()
		defer h.updateMu.Unlock()
		h.mu.Lock()
		stopping := h.stopping
		h.mu.Unlock()
		if stopping {
			return Response{OK: false, Message: "service host is stopping", Stage: "stopped"}
		}
		return h.rollbackUpdate(req.Params)
	default:
		return Response{OK: false, Message: "unknown or forbidden action"}
	}
}

func (h *Host) watchControlFile(ctx context.Context) {
	reqPath := filepath.Join(h.StateDir, "control-request.json")
	respPath := filepath.Join(h.StateDir, "control-response.json")
	t := time.NewTicker(200 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			b, err := os.ReadFile(reqPath)
			if err != nil || len(b) == 0 {
				continue
			}
			_ = os.Remove(reqPath)
			var req Request
			if json.Unmarshal(b, &req) != nil {
				continue
			}
			resp := h.dispatch(req, false)
			raw, _ := json.Marshal(resp)
			tmp := respPath + ".tmp"
			if os.WriteFile(tmp, raw, 0o600) == nil {
				_ = os.Rename(tmp, respPath)
			}
		}
	}
}

func (h *Host) targetBin(component string) string {
	if component == "service_host" && h.SelfBin != "" {
		return h.SelfBin
	}
	return h.WorkerBin
}

func (h *Host) activateUpdate(params map[string]string) Response {
	src := params["path"]
	expect := params["sha256"]
	component := params["component"]
	if component == "" {
		component = "worker"
	}
	if component != "worker" {
		return Response{OK: false, Message: "service-host self replacement requires an independent native recovery installer; only worker activation is supported", Stage: "unsupported_component"}
	}
	if src == "" || expect == "" {
		return Response{OK: false, Message: "path and sha256 are required", Stage: "blocked"}
	}
	target := h.targetBin(component)
	if target == "" {
		return Response{OK: false, Message: "target binary path is not configured", Stage: "blocked"}
	}
	staged, err := update.StageBinary(h.StateDir, src, expect)
	if err != nil {
		return Response{OK: false, Message: err.Error(), Stage: string(update.StageIdle)}
	}
	oldSHA, err := update.SHA256File(target)
	if err != nil {
		return Response{OK: false, Message: "cannot verify current worker before update: " + err.Error()}
	}
	newSHA, err := update.SHA256File(staged)
	if err != nil {
		return Response{OK: false, Message: err.Error()}
	}
	j := &update.Journal{
		TxID: idgen.New(), Stage: update.StageStaged, OldPath: target, NewPath: staged,
		Current: target, PrevPath: filepath.Join(h.StateDir, "updates", "prev.bin"),
		OldSHA: oldSHA, NewSHA: newSHA, StartedAt: time.Now().UTC(),
	}
	if err := j.Save(h.StateDir); err != nil {
		return Response{OK: false, Message: err.Error()}
	}
	j.Stage = update.StageSwitching
	if err := j.Save(h.StateDir); err != nil {
		return Response{OK: false, Message: err.Error()}
	}
	if component == "worker" {
		h.stopWorker()
	}
	if err := update.Activate(target, staged, j.PrevPath); err != nil {
		return h.failedActivation(j, "activation failed: "+err.Error())
	}
	j.Stage = update.StageProbation
	if err := j.Save(h.StateDir); err != nil {
		return h.failedActivation(j, "probation journal could not be persisted")
	}
	if component == "worker" {
		if err := h.startWorker(); err != nil {
			return h.failedActivation(j, "candidate start failed: "+err.Error())
		}
		wait := h.Probation
		if wait <= 0 {
			wait = 2 * time.Second
		}
		h.mu.Lock()
		candidate, done := h.cmd, h.done
		h.mu.Unlock()
		timer := time.NewTimer(wait)
		select {
		case <-done:
		case <-timer.C:
		}
		timer.Stop()
		h.mu.Lock()
		alive := h.alive && h.cmd == candidate
		select {
		case <-done:
			alive = false
		default:
		}
		h.mu.Unlock()
		if !alive {
			return h.failedActivation(j, "new worker failed local probation")
		}
	}
	j.Stage = update.StageConfirmed
	if err := j.Save(h.StateDir); err != nil {
		return Response{OK: false, Message: err.Error(), Stage: string(update.StageProbation)}
	}
	return Response{OK: true, Message: "activated", Stage: string(j.Stage)}
}

func (h *Host) failedActivation(j *update.Journal, reason string) Response {
	h.stopWorker()
	if err := h.restoreJournaledWorker(j); err != nil {
		return Response{OK: false, Message: reason + "; recovery could not be verified: " + err.Error(), Stage: "recovery_failed"}
	}
	j.Stage = update.StageRollback
	j.Reason = reason
	if err := j.Save(h.StateDir); err != nil {
		_ = h.startWorker() // Best effort availability; never claim durable recovery.
		return Response{OK: false, Message: reason + "; recovery journal could not be persisted", Stage: "recovery_failed"}
	}
	h.mu.Lock()
	stopping := h.stopping
	h.mu.Unlock()
	if !stopping {
		if err := h.startWorker(); err != nil {
			return Response{OK: false, Message: reason + "; previous worker did not start: " + err.Error(), Stage: "recovery_failed"}
		}
	}
	return Response{OK: false, Message: reason, Stage: string(update.StageRollback)}
}

func (h *Host) restoreJournaledWorker(j *update.Journal) error {
	if j.Current != h.WorkerBin || j.PrevPath != filepath.Join(h.StateDir, "updates", "prev.bin") || len(j.OldSHA) != 64 {
		return fmt.Errorf("incomplete previous-good identity")
	}
	current, err := update.SHA256File(j.Current)
	if err == nil && current == j.OldSHA {
		return nil
	}
	return update.RestoreVerified(j.Current, j.PrevPath, j.OldSHA)
}

func (h *Host) rollbackUpdate(params map[string]string) Response {
	if component := params["component"]; component != "" && component != "worker" {
		return Response{OK: false, Message: "unsupported rollback component"}
	}
	j, err := update.Load(h.StateDir)
	if err != nil {
		return Response{OK: false, Message: err.Error()}
	}
	if j.Current != h.WorkerBin || j.PrevPath != filepath.Join(h.StateDir, "updates", "prev.bin") {
		return Response{OK: false, Message: "recovery journal does not identify this managed worker"}
	}
	if j.PrevPath == "" || j.Stage == update.StageIdle || len(j.OldSHA) != 64 {
		return Response{OK: false, Message: "no previous-good slot in the recovery journal"}
	}
	if expect := params["sha256"]; expect != "" && j.OldSHA != "" && expect != j.OldSHA {
		return Response{OK: false, Message: "rollback digest is not the journaled previous-good build"}
	}
	sum, err := update.SHA256File(j.PrevPath)
	if err != nil {
		return Response{OK: false, Message: err.Error()}
	}
	if j.OldSHA != "" && sum != j.OldSHA {
		return Response{OK: false, Message: "previous-good slot digest mismatch"}
	}
	component := params["component"]
	target := h.targetBin(component)
	if j.Current != "" {
		target = j.Current
	}
	h.stopWorker()
	if err := update.RestoreVerified(target, j.PrevPath, j.OldSHA); err != nil {
		_ = h.startWorker()
		return Response{OK: false, Message: err.Error()}
	}
	j.Stage = update.StageRollback
	if err := j.Save(h.StateDir); err != nil {
		_ = h.startWorker()
		return Response{OK: false, Message: "previous bytes restored but recovery journal was not persisted", Stage: "recovery_failed"}
	}
	if err := h.startWorker(); err != nil {
		return Response{OK: false, Message: err.Error(), Stage: string(update.StageRollback)}
	}
	return Response{OK: true, Message: "rolled back", Stage: string(update.StageRollback)}
}

func (h *Host) recoverJournal() error {
	j, err := update.Load(h.StateDir)
	if err != nil {
		return err
	}
	switch j.Stage {
	case update.StageSwitching, update.StageProbation:
		if j.Current != h.WorkerBin || j.PrevPath != filepath.Join(h.StateDir, "updates", "prev.bin") || j.OldSHA == "" {
			return fmt.Errorf("invalid worker recovery journal")
		}
		current, _ := update.SHA256File(h.WorkerBin)
		if current != j.OldSHA {
			previous, err := update.SHA256File(j.PrevPath)
			if err != nil || previous != j.OldSHA {
				return fmt.Errorf("previous-good binary cannot be verified")
			}
			if err := update.RestoreVerified(h.WorkerBin, j.PrevPath, j.OldSHA); err != nil {
				return err
			}
		}
		j.Stage = update.StageRollback
		j.Reason = "recovered incomplete transaction on service-host start"
		return j.Save(h.StateDir)
	}
	return nil
}

func (h *Host) startWorker() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.stopping {
		return fmt.Errorf("service host is stopping")
	}
	if h.alive {
		return nil
	}
	cmd := exec.Command(h.WorkerBin, "run", "--config", h.WorkerCfg)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	h.cmd = cmd
	h.alive = true
	h.done = make(chan struct{})
	go func(c *exec.Cmd, done chan struct{}) {
		_ = c.Wait()
		h.mu.Lock()
		if h.cmd == c {
			h.alive = false
		}
		// A closed done channel now guarantees that alive has been cleared.
		close(done)
		h.mu.Unlock()
	}(cmd, h.done)
	return nil
}

func (h *Host) stopWorker() {
	h.mu.Lock()
	cmd := h.cmd
	done := h.done
	h.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return
	}
	if runtime.GOOS == "windows" {
		_ = cmd.Process.Kill()
	} else {
		_ = cmd.Process.Signal(os.Interrupt)
	}
	if done != nil {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
	}
	h.mu.Lock()
	h.alive = false
	h.mu.Unlock()
}

func (h *Host) restartWorker() error {
	h.stopWorker()
	return h.startWorker()
}

func (h *Host) reap(ctx context.Context) {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	backoff := time.Second
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			h.mu.Lock()
			alive := h.alive
			h.mu.Unlock()
			if alive {
				backoff = time.Second
				continue
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if ctx.Err() != nil {
				return
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
			// A worker intentionally stopped for activation must not be resurrected
			// by the reaper while its binary is being replaced.
			if h.updateMu.TryLock() {
				if ctx.Err() == nil {
					_ = h.startWorker()
				}
				h.updateMu.Unlock()
			}
		}
	}
}

func PlanUnit(bin, cfg, user string) string {
	return PlanUnitState(bin, cfg, "/var/lib/monik-agent", user)
}

func PlanUnitState(bin, cfg, state, user string) string {
	agent := filepath.Join(filepath.Dir(bin), "monik-agent")
	if runtime.GOOS == "windows" {
		agent += ".exe"
	}
	return PlanManagedUnit(bin, agent, cfg, state, user)
}

func PlanManagedUnit(bin, agent, cfg, state, user string) string {
	if state == "" {
		state = "/var/lib/monik-agent"
	}
	return fmt.Sprintf(`[Unit]
Description=Monik agent service host
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=%s run --worker %s --config %s --state %s
Restart=on-failure
RestartSec=5
User=%s
NoNewPrivileges=true
# Raw-socket capability for ICMP fallback; no root worker or global sysctl change.
CapabilityBoundingSet=CAP_NET_RAW
AmbientCapabilities=CAP_NET_RAW
UMask=0077
KillMode=control-group
TimeoutStopSec=15

[Install]
WantedBy=multi-user.target
`, unitArgument(bin), unitArgument(agent), unitArgument(cfg), unitArgument(state), user)
}

func PlanWindowsService(hostBin, workerBin, cfg string) string {
	return fmt.Sprintf(`sc.exe create MonikAgent binPath= "%s run --worker %s --config %s --state C:\\ProgramData\\Monik\\agent" start= auto
sc.exe description MonikAgent "Monik agent service host"
sc.exe start MonikAgent
`, hostBin, workerBin, cfg)
}

// systemd does not use shell quoting; protect its own specifier/environment expansion.
func unitArgument(value string) string {
	return strconv.Quote(strings.NewReplacer("%", "%%", "$", "$$").Replace(value))
}
