package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSparkInstallSingleWorkerAndIndependentUnit(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(t.TempDir(), "spark-worker")
	if err := os.WriteFile(src, []byte("light-worker"), 0755); err != nil {
		t.Fatal(err)
	}
	o := Options{Prefix: filepath.Join(root, "prefix"), StateDir: filepath.Join(root, "state"), UnitPath: filepath.Join(root, "monik-agent.service"), WorkerSrc: src, SkipSystemctl: true}
	if err := InstallSparkLinux(o); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(o.Prefix, "monik-service-host")); !os.IsNotExist(err) {
		t.Fatal("resident supervisor created", err)
	}
	b, err := os.ReadFile(o.UnitPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"User=monik", "CapabilityBoundingSet=\n", "NoNewPrivileges=true", "ProtectSystem=strict", " run --config "} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("missing %s", want)
		}
	}
	for _, forbidden := range []string{"Requires=", "PartOf=", "CAP_NET_RAW", "service-host", "qwen", "sglang"} {
		if strings.Contains(string(b), forbidden) {
			t.Fatalf("model/full runtime coupling %s", forbidden)
		}
	}
}

func TestSparkInstallInvalidSourcePreservesCurrentWorker(t *testing.T) {
	root := t.TempDir()
	o := Options{Prefix: filepath.Join(root, "prefix"), StateDir: filepath.Join(root, "state"), UnitPath: filepath.Join(root, "monik-agent.service"), WorkerSrc: filepath.Join(root, "absent"), SkipSystemctl: true}
	if err := os.MkdirAll(filepath.Join(o.StateDir, "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(o.StateDir, "bin", "monik-agent")
	if err := os.WriteFile(p, []byte("existing"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := InstallSparkLinux(o); err == nil {
		t.Fatal("missing source accepted")
	}
	b, err := os.ReadFile(p)
	if err != nil || string(b) != "existing" {
		t.Fatal("old worker damaged", err)
	}
}
