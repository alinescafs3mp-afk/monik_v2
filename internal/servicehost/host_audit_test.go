package servicehost

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/update"
)

func TestAuditUnsignedUpdateCannotReachPrivilegedActivation(t *testing.T) {
	for _, action := range []string{"activate_update", "rollback_update", "execute_shell"} {
		t.Run(action, func(t *testing.T) {
			dir := t.TempDir()
			target := filepath.Join(dir, "worker")
			os.WriteFile(target, []byte("original"), 0600)
			h := &Host{WorkerBin: target, StateDir: dir}
			server, client := net.Pipe()
			defer client.Close()
			go h.handle(server)
			if err := json.NewEncoder(client).Encode(Request{Action: action, Params: map[string]string{"path": "/tmp/untrusted"}}); err != nil {
				t.Fatal(err)
			}
			var reply Response
			if err := json.NewDecoder(client).Decode(&reply); err != nil {
				t.Fatal(err)
			}
			if reply.OK {
				t.Fatal("unsafe update accepted")
			}
			b, _ := os.ReadFile(target)
			if string(b) != "original" {
				t.Fatal("worker modified")
			}
		})
	}
}

func TestActivateVerifiedWorkerPassesProbation(t *testing.T) {
	dir := t.TempDir()
	script := "#!/bin/sh\nexec sleep 30\n"
	worker := filepath.Join(dir, "worker")
	if err := os.WriteFile(worker, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	incoming := filepath.Join(dir, "incoming")
	next := "#!/bin/sh\nexec sleep 30\n"
	if err := os.WriteFile(incoming, []byte(next), 0o755); err != nil {
		t.Fatal(err)
	}
	sum, err := update.SHA256File(incoming)
	if err != nil {
		t.Fatal(err)
	}
	h := &Host{WorkerBin: worker, WorkerCfg: filepath.Join(dir, "cfg"), StateDir: dir, Probation: 40 * time.Millisecond}
	if err := h.startWorker(); err != nil {
		t.Fatal(err)
	}
	defer h.stopWorker()
	server, client := net.Pipe()
	defer client.Close()
	go h.handle(server)
	if err := json.NewEncoder(client).Encode(Request{Action: "activate_update", Params: map[string]string{"path": incoming, "sha256": sum}}); err != nil {
		t.Fatal(err)
	}
	var reply Response
	if err := json.NewDecoder(client).Decode(&reply); err != nil {
		t.Fatal(err)
	}
	if !reply.OK || reply.Stage != string(update.StageConfirmed) {
		t.Fatalf("%+v", reply)
	}
	b, _ := os.ReadFile(worker)
	if string(b) != next {
		t.Fatal("verified worker was not installed")
	}
}
