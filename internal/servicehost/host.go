package servicehost

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/idgen"
	"github.com/alinescafs3mp-afk/monik_v2/internal/update"
)

// Host supervises the collector worker and performs typed update transactions.
// It is not a general command runner.
type Host struct {
	WorkerBin string
	WorkerCfg string
	StateDir  string
	SockPath  string
	cmd       *exec.Cmd
	alive     bool
	done      chan struct{}
	mu        sync.Mutex
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
	defer ln.Close()
	if err := h.startWorker(); err != nil {
		return err
	}
	go h.reap(ctx)
	go func() {
		<-ctx.Done()
		_ = ln.Close()
		h.stopWorker()
	}()
	for {
		c, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			continue
		}
		go h.handle(c)
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
	if err := json.NewDecoder(c).Decode(&req); err != nil {
		return
	}
	var resp Response
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
		resp = Response{OK: true, Message: msg, PID: pid}
	case "restart_worker":
		if runtime.GOOS == "windows" {
			resp = Response{OK: false, Message: "unauthenticated TCP control cannot authorize restart"}
			break
		}
		if err := h.restartWorker(); err != nil {
			resp = Response{OK: false, Message: err.Error()}
		} else {
			resp = Response{OK: true, Message: "restarted"}
		}
	case "activate_update":
		resp = Response{OK: false, Message: "update activation disabled: trusted TUF verification and probation are not implemented", Stage: "blocked"}
	case "rollback_update":
		resp = Response{OK: false, Message: "remote rollback disabled: eligible signed rollback verification is not implemented", Stage: "blocked"}
	default:
		resp = Response{OK: false, Message: "unknown or forbidden action"}
	}
	_ = json.NewEncoder(c).Encode(resp)
}

func (h *Host) activateUpdate(params map[string]string) Response {
	src := params["path"]
	expect := params["sha256"]
	if src == "" {
		return Response{OK: false, Message: "path required"}
	}
	staged, err := update.StageBinary(h.StateDir, src, expect)
	if err != nil {
		return Response{OK: false, Message: err.Error(), Stage: string(update.StageIdle)}
	}
	oldSHA, _ := update.SHA256File(h.WorkerBin)
	newSHA, _ := update.SHA256File(staged)
	j := &update.Journal{
		TxID: idgen.New(), Stage: update.StageStaged, OldPath: h.WorkerBin, NewPath: staged,
		Current: h.WorkerBin, PrevPath: filepath.Join(h.StateDir, "updates", "prev.bin"),
		OldSHA: oldSHA, NewSHA: newSHA, StartedAt: time.Now().UTC(),
	}
	if err := j.Save(h.StateDir); err != nil {
		return Response{OK: false, Message: err.Error()}
	}
	j.Stage = update.StageSwitching
	_ = j.Save(h.StateDir)
	h.stopWorker()
	if err := update.Activate(h.WorkerBin, staged, j.PrevPath); err != nil {
		j.Stage = update.StageRollback
		j.Reason = err.Error()
		_ = j.Save(h.StateDir)
		_ = update.Rollback(h.WorkerBin, j.PrevPath)
		_ = h.startWorker()
		return Response{OK: false, Message: err.Error(), Stage: string(update.StageRollback)}
	}
	j.Stage = update.StageProbation
	_ = j.Save(h.StateDir)
	if err := h.startWorker(); err != nil {
		_ = update.Rollback(h.WorkerBin, j.PrevPath)
		j.Stage = update.StageRollback
		j.Reason = err.Error()
		_ = j.Save(h.StateDir)
		_ = h.startWorker()
		return Response{OK: false, Message: err.Error(), Stage: string(update.StageRollback)}
	}
	j.Stage = update.StageConfirmed
	_ = j.Save(h.StateDir)
	return Response{OK: true, Message: "activated", Stage: string(j.Stage)}
}

func (h *Host) rollbackUpdate() Response {
	j, err := update.Load(h.StateDir)
	if err != nil {
		return Response{OK: false, Message: err.Error()}
	}
	h.stopWorker()
	if err := update.Rollback(h.WorkerBin, j.PrevPath); err != nil {
		_ = h.startWorker()
		return Response{OK: false, Message: err.Error()}
	}
	j.Stage = update.StageRollback
	_ = j.Save(h.StateDir)
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
		if j.PrevPath != "" {
			_ = update.Rollback(h.WorkerBin, j.PrevPath)
			j.Stage = update.StageRollback
			j.Reason = "recovered incomplete transaction on service-host start"
			_ = j.Save(h.StateDir)
		}
	}
	return nil
}

func (h *Host) startWorker() error {
	h.mu.Lock()
	defer h.mu.Unlock()
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
		close(done)
		h.mu.Lock()
		if h.cmd == c {
			h.alive = false
		}
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
			_ = h.startWorker()
		}
	}
}

func PlanUnit(bin, cfg, user string) string {
	agent := filepath.Join(filepath.Dir(bin), "monik-agent")
	if runtime.GOOS == "windows" {
		agent += ".exe"
	}
	return fmt.Sprintf(`[Unit]
Description=Monik agent service host
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=%s run --worker %s --config %s --state /var/lib/monik-agent
Restart=on-failure
RestartSec=5
User=%s
NoNewPrivileges=true

[Install]
WantedBy=multi-user.target
`, bin, agent, cfg, user)
}

func PlanWindowsService(hostBin, workerBin, cfg string) string {
	return fmt.Sprintf(`sc.exe create MonikAgent binPath= "%s run --worker %s --config %s --state C:\\ProgramData\\Monik\\agent" start= auto
sc.exe description MonikAgent "Monik agent service host"
sc.exe start MonikAgent
`, hostBin, workerBin, cfg)
}
