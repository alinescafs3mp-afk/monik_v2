//go:build linux

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/agent/configfile"
	"github.com/alinescafs3mp-afk/monik_v2/internal/agent/setup"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"github.com/alinescafs3mp-afk/monik_v2/internal/servicehost"
)

// Opt-in integration runs the separately compiled native worker under an
// unprivileged UID. It does not install services or claim boot/power-loss evidence.
func TestAudit8NativeWorkerRestartAndRejectedUpload(t *testing.T) { runNativeSmoke(t, false) }
func TestAudit8NativeSupervisorRestartsWorker(t *testing.T)       { runNativeSmoke(t, true) }
func runNativeSmoke(t *testing.T, withSupervisor bool) {
	bin := os.Getenv("MONIK_NATIVE_AGENT_BIN")
	if bin == "" {
		t.Skip("build agent and set MONIK_NATIVE_AGENT_BIN for native process evidence")
	}
	if withSupervisor && os.Getenv("MONIK_NATIVE_SUPERVISOR_BIN") == "" {
		t.Skip("build supervisor and set MONIK_NATIVE_SUPERVISOR_BIN")
	}
	a, h := testApp(t)
	var refuse atomic.Bool
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if refuse.Load() && (r.URL.Path == "/api/v1/agent/report" || r.URL.Path == "/api/v1/agent/control") {
			http.Error(w, "synthetic upload outage", 503)
			return
		}
		h.ServeHTTP(w, r)
	}))
	defer ts.Close()
	root, e := os.MkdirTemp("", "monik-native-audit-")
	if e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(root)
	os.Chmod(root, 0711)
	data := filepath.Join(root, "state")
	code, _, e := a.Store.CreateEnrollmentCode("owner", time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	st, e := setup.Enroll(&setup.Profile{ControllerURL: ts.URL, EnrollmentCode: code, StateDir: data, CACertPEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ts.Certificate().Raw})), DisplayName: "Real process smoke"})
	if e != nil {
		t.Fatal(e)
	}
	cfg := protocol.DefaultAgentConfig()
	cfg.Ping.Enabled = false
	cfg.Collectors.Ping = false
	hash := configfile.HashConfig(cfg)
	raw, _ := json.Marshal(cfg)
	if e = a.Store.SetDesired(st.File.AgentID, 2, hash, string(raw)); e != nil {
		t.Fatal(e)
	}
	st.File.Managed = withSupervisor
	st.File.AppliedRevision = 2
	st.File.AppliedHash = hash
	if e = st.Save(); e != nil {
		t.Fatal(e)
	}
	if e = configfile.SaveAppliedConfig(data, cfg, 2, hash); e != nil {
		t.Fatal(e)
	}
	exe := filepath.Join(root, "monik-agent")
	b, e := os.ReadFile(bin)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(exe, b, 0755); e != nil {
		t.Fatal(e)
	}
	launchBin, launchArgs := exe, []string{"run", "--config", filepath.Join(data, "agent.json")}
	if withSupervisor {
		supervisor := filepath.Join(root, "monik-service-host")
		bin, e := os.ReadFile(os.Getenv("MONIK_NATIVE_SUPERVISOR_BIN"))
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(supervisor, bin, 0755); e != nil {
			t.Fatal(e)
		}
		launchBin = supervisor
		launchArgs = []string{"run", "--worker", exe, "--config", filepath.Join(data, "agent.json"), "--state", data}
	}
	var credential *syscall.Credential
	if os.Geteuid() == 0 {
		credential = &syscall.Credential{Uid: 65534, Gid: 65534}
		if e = filepath.Walk(data, func(p string, i os.FileInfo, e error) error {
			if e != nil {
				return e
			}
			return os.Chown(p, 65534, 65534)
		}); e != nil {
			t.Fatal(e)
		}
	}
	start := func() (*exec.Cmd, context.CancelFunc, *bytes.Buffer) {
		ctx, cancel := context.WithCancel(context.Background())
		cmd := exec.CommandContext(ctx, launchBin, launchArgs...)
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: credential}
		cmd.Env = []string{"HOME=" + data, "PATH=/usr/bin:/bin"}
		log := &bytes.Buffer{}
		cmd.Stdout = log
		cmd.Stderr = log
		if e := cmd.Start(); e != nil {
			cancel()
			t.Fatal(e)
		}
		return cmd, cancel, log
	}
	stop := func(cmd *exec.Cmd, cancel context.CancelFunc) {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case e := <-done:
			if e != nil {
				t.Errorf("worker did not exit cleanly: %v", e)
			}
		case <-time.After(8 * time.Second):
			cancel()
			<-done
			t.Error("worker stop timed out")
		}
		cancel()
	}
	wait := func(label string, cond func() bool) {
		until := time.Now().Add(18 * time.Second)
		for time.Now().Before(until) {
			if cond() {
				return
			}
			time.Sleep(150 * time.Millisecond)
		}
		t.Fatalf("%s timed out", label)
	}
	count := func() int {
		var n int
		_ = a.Store.DB.QueryRow("SELECT COUNT(*) FROM host_samples WHERE agent_id=?", st.File.AgentID).Scan(&n)
		return n
	}
	cmd, cancel, _ := start()
	defer func() { cancel(); _ = cmd.Process.Kill() }()
	wait("two real measurements", func() bool { return count() >= 2 })
	sample, _, e := a.Store.LatestHost(st.File.AgentID)
	if e != nil || sample.Hostname == "" || sample.RAMTotal <= 0 || sample.CPUPercent == nil {
		t.Fatal("missing real native metrics", e)
	}
	if withSupervisor {
		status, e := servicehost.Call(data, servicehost.Request{Action: "status"})
		if e != nil || status.PID <= 1 {
			t.Fatal("supervisor status", e)
		}
		worker, e := os.FindProcess(status.PID)
		if e != nil {
			t.Fatal(e)
		}
		if e = worker.Kill(); e != nil {
			t.Fatal(e)
		}
		wait("supervisor replaces terminated worker", func() bool {
			r, e := servicehost.Call(data, servicehost.Request{Action: "status"})
			return e == nil && r.PID > 1 && r.PID != status.PID
		})
	}
	stop(cmd, cancel)
	before := count()
	cmd, cancel, _ = start()
	wait("same identity after restart", func() bool { return count() > before })
	var sessions int
	_ = a.Store.DB.QueryRow("SELECT COUNT(DISTINCT session_id) FROM host_samples WHERE agent_id=?", st.File.AgentID).Scan(&sessions)
	if sessions < 2 {
		t.Fatal("new process session not recorded")
	}
	refuse.Store(true)
	spoolRecords := func() int {
		n := 0
		_ = filepath.Walk(filepath.Join(data, "spool"), func(p string, i os.FileInfo, e error) error {
			if e == nil && !i.IsDir() && filepath.Ext(p) == ".json" {
				n++
			}
			return nil
		})
		return n
	}
	wait("durable offline spool", func() bool { return spoolRecords() > 0 })
	refuse.Store(false)
	wait("spool drain after recovery", func() bool { return spoolRecords() == 0 })
	after, e := configfile.Load(filepath.Join(data, "agent.json"))
	if e != nil || after.File.AgentID != st.File.AgentID || after.File.ControllerURL != ts.URL {
		t.Fatal("identity/selected URL changed", e)
	}
	stop(cmd, cancel)
	fmt.Printf("NATIVE SMOKE PASS: supervisor=%v uid=%d; real CPU/RAM; clean stop; restart; 503 upload outage; durable spool drain; same identity/URL. Not systemd/SCM/SSH-shell acceptance.\n", withSupervisor, func() int {
		if credential != nil {
			return int(credential.Uid)
		}
		return os.Geteuid()
	}())
}
