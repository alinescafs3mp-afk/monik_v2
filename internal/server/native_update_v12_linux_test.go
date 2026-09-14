//go:build linux

package server

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/agent/configfile"
	"github.com/alinescafs3mp-afk/monik_v2/internal/agent/setup"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"github.com/alinescafs3mp-afk/monik_v2/internal/secure"
)

// Opt-in real-binary acceptance: signed delivery, supervised activation,
// new-session evidence, rollout observation and recovery of a failing candidate.
// No service is installed, no public host contacted and no OS reboot claimed.
func TestV12NativeSignedUpdateAndFailedCandidateRecovery(t *testing.T) {
	workerSrc, hostSrc := os.Getenv("MONIK_NATIVE_AGENT_BIN"), os.Getenv("MONIK_NATIVE_SUPERVISOR_BIN")
	if workerSrc == "" || hostSrc == "" {
		t.Skip("build native binaries and set MONIK_NATIVE_* variables")
	}
	workerBytes, e := os.ReadFile(workerSrc)
	if e != nil {
		t.Fatal(e)
	}
	supervisorBytes, e := os.ReadFile(hostSrc)
	if e != nil {
		t.Fatal(e)
	}
	a, h := testApp(t)
	ts := httptest.NewTLSServer(h)
	defer ts.Close()
	keys, repo := t.TempDir(), t.TempDir()
	if op := importRelease10(t, a, releaseBundle10(t, keys, repo, string(workerBytes), "V12-A"), "native-initial"); op.Status != protocol.OpCompleted {
		t.Fatal("initial signed trust", op)
	}
	root, e := os.MkdirTemp("", "monik-native-update-")
	if e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(root)
	if e = os.Chmod(root, 0711); e != nil {
		t.Fatal(e)
	}
	data := filepath.Join(root, "state")
	code, _, e := a.Store.CreateEnrollmentCode("owner", time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	st, e := setup.Enroll(&setup.Profile{ControllerURL: ts.URL, EnrollmentCode: code, StateDir: data, CACertPEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ts.Certificate().Raw})), DisplayName: "V12 real signed upgrade"})
	if e != nil {
		t.Fatal(e)
	}
	cfg := protocol.DefaultAgentConfig()
	cfg.Paused = true
	cfg.Ping.Enabled = false
	cfg.Collectors.Ping = false
	raw, _ := json.Marshal(cfg)
	hash := configfile.HashConfig(cfg)
	if e = a.Store.SetDesired(st.File.AgentID, 2, hash, string(raw)); e != nil {
		t.Fatal(e)
	}
	st.File.Managed = true
	st.File.AppliedRevision = 2
	st.File.AppliedHash = hash
	if e = st.Save(); e != nil {
		t.Fatal(e)
	}
	if e = configfile.SaveAppliedConfig(data, cfg, 2, hash); e != nil {
		t.Fatal(e)
	}
	worker := filepath.Join(data, "bin", "monik-agent")
	if e = os.MkdirAll(filepath.Dir(worker), 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(worker, workerBytes, 0755); e != nil {
		t.Fatal(e)
	}
	host := filepath.Join(root, "monik-service-host")
	if e = os.WriteFile(host, supervisorBytes, 0755); e != nil {
		t.Fatal(e)
	}
	var creds *syscall.Credential
	if os.Geteuid() == 0 {
		creds = &syscall.Credential{Uid: 65534, Gid: 65534}
		if e = filepath.Walk(data, func(p string, i os.FileInfo, e error) error {
			if e != nil {
				return e
			}
			return os.Chown(p, 65534, 65534)
		}); e != nil {
			t.Fatal(e)
		}
	}
	logPath := filepath.Join(root, "process.log")
	log, e := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	defer log.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, host, "run", "--worker", worker, "--config", filepath.Join(data, "agent.json"), "--state", data)
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: creds}
	cmd.Env = []string{"HOME=" + data, "PATH=/usr/bin:/bin"}
	cmd.Stdout = log
	cmd.Stderr = log
	if e = cmd.Start(); e != nil {
		t.Fatal(e)
	}
	stopped := make(chan error, 1)
	go func() { stopped <- cmd.Wait() }()
	defer func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case err := <-stopped:
			if err != nil {
				t.Errorf("supervisor stop: %v", err)
			}
		case <-time.After(10 * time.Second):
			cancel()
			<-stopped
			t.Error("supervisor did not stop")
		}
		if t.Failed() {
			b, _ := os.ReadFile(logPath)
			t.Logf("native process: %s", b)
		}
	}()
	wait := func(label string, test func() bool) {
		t.Helper()
		until := time.Now().Add(65 * time.Second)
		for time.Now().Before(until) {
			if test() {
				return
			}
			time.Sleep(150 * time.Millisecond)
		}
		t.Fatalf("timed out: %s", label)
	}
	wait("managed worker live", func() bool {
		ag, e := a.Store.Agent(st.File.AgentID)
		return e == nil && ag.ManagedReady && ag.LastLiveAt != nil
	})
	old, e := a.Store.Agent(st.File.AgentID)
	if e != nil {
		t.Fatal(e)
	}
	oldSession := old.SessionID
	// Appending an inert trailer preserves this ELF's entry point while making
	// an independently signed target with a different digest. It is not a second
	// product version claim; the process and bytes really are replaced.
	candidate := append(append([]byte{}, workerBytes...), []byte("\nMONIK_V12_SIGNED_CANDIDATE\n")...)
	candidateHash := secure.SHA256Bytes(candidate)
	publishAndStart := func(payload []byte, version, key string) string {
		t.Helper()
		op := importRelease10(t, a, releaseBundle10(t, keys, repo, string(payload), version), key+"-import")
		if op.Status != protocol.OpCompleted {
			t.Fatalf("import failed: %+v", op)
		}
		releases, e := a.Store.Releases()
		if e != nil {
			t.Fatal(e)
		}
		releaseID := ""
		for _, rel := range releases {
			if rel["version"] == version {
				releaseID = rel["id"].(string)
			}
		}
		if releaseID == "" {
			t.Fatal("published release missing")
		}
		w := httptest.NewRecorder()
		a.processSubmit(w, ownerRecent(t), protocol.SubmitOperation{Action: "update.rollout", ClientRequestKey: key, TargetIDs: []string{st.File.AgentID}, Params: map[string]any{"release_id": releaseID, "batch_size": float64(1), "observe_seconds": float64(15)}})
		if w.Code >= 400 || json.Unmarshal(w.Body.Bytes(), &op) != nil {
			t.Fatalf("rollout rejected: %d %s", w.Code, w.Body.String())
		}
		if op.Status == protocol.OpCompletedWithErrs || len(op.Targets) != 1 {
			t.Fatalf("preflight failed: %+v", op)
		}
		return op.ID
	}
	first := publishAndStart(candidate, "V12-B", "native-upgrade")
	wait("real candidate confirmed and observed", func() bool {
		if e := a.Store.AdvanceRollouts(); e != nil {
			t.Fatal(e)
		}
		op, e := a.Store.Operation(first)
		if e != nil {
			t.Fatal(e)
		}
		if op.Status == protocol.OpCompletedWithErrs || op.Status == protocol.OpAttentionRequired {
			t.Fatalf("native upgrade did not succeed: %+v", op)
		}
		return op.Status == protocol.OpCompleted
	})
	current, e := a.Store.Agent(st.File.AgentID)
	if e != nil || current.WorkerDigest != candidateHash || current.SessionID == oldSession {
		t.Fatalf("not a confirmed new process: %+v %v", current, e)
	}
	oldSession = current.SessionID
	second := publishAndStart([]byte("#!/bin/sh\nexit 1\n"), "V12-C-broken", "native-rollback")
	wait("real failed candidate rolls back", func() bool {
		if e := a.Store.AdvanceRollouts(); e != nil {
			t.Fatal(e)
		}
		op, e := a.Store.Operation(second)
		if e != nil {
			t.Fatal(e)
		}
		for _, target := range op.Targets {
			if target.Status == protocol.TargetRolledBack {
				return true
			}
		}
		return false
	})
	current, e = a.Store.Agent(st.File.AgentID)
	if e != nil || current.WorkerDigest != candidateHash || current.SessionID == oldSession {
		t.Fatalf("previous verified worker not restored: %+v %v", current, e)
	}
	r, e := a.Store.Rollout(second)
	if e != nil || r.State != "blocked" {
		t.Fatalf("failed update did not block rollout: %+v %v", r, e)
	}
	if b, e := os.ReadFile(worker); e != nil || secure.SHA256Bytes(b) != candidateHash {
		t.Fatal("wrong restored executable")
	}
	if !strings.HasPrefix(current.WorkerDigest, candidateHash) {
		t.Fatal("digest changed unexpectedly")
	}
	uid := os.Geteuid()
	if creds != nil {
		uid = int(creds.Uid)
	}
	t.Logf("real signed Linux upgrade and failed-candidate recovery passed: uid=%d, agent_id preserved, new sessions verified, original signature trust retained", uid)
}
