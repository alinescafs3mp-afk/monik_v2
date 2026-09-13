package servicehost

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
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
