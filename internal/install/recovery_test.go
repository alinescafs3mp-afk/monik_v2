package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/alinescafs3mp-afk/monik_v2/internal/agent/configfile"
	"github.com/alinescafs3mp-afk/monik_v2/internal/secure"
)

func recoveryFixture(t *testing.T) (Options, configfile.File) {
	t.Helper()
	root := t.TempDir()
	o := Options{Prefix: filepath.Join(root, "prefix"), StateDir: filepath.Join(root, "state"), UnitPath: filepath.Join(root, "monik-agent.service"), SkipSystemctl: true}
	o.ConfigPath = filepath.Join(o.StateDir, "agent.json")
	for _, p := range []string{o.Prefix, filepath.Join(o.StateDir, "bin")} {
		if err := os.MkdirAll(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for p, body := range map[string]string{filepath.Join(o.Prefix, "monik-agent"): "old-worker", filepath.Join(o.StateDir, "bin", "monik-agent"): "old-worker", filepath.Join(o.Prefix, "monik-service-host"): "old-host", o.UnitPath: "old-independent-unit"} {
		if err := os.WriteFile(p, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	f := configfile.File{AgentID: "same-agent", ControllerID: "same-controller", ControllerURL: "https://original", StateDir: o.StateDir, CredentialPath: filepath.Join(o.StateDir, "credential"), Managed: true, EndpointGeneration: 2}
	b, _ := json.Marshal(f)
	if err := os.WriteFile(o.ConfigPath, b, 0600); err != nil {
		t.Fatal(err)
	}
	if err := saveRecovery(o, f, []byte("new-worker"), nil, "new-unit", true); err != nil {
		t.Fatal(err)
	}
	return o, f
}
func TestLocalRollbackKeepsNewEnrollmentGenerationAndData(t *testing.T) {
	o, f := recoveryFixture(t)
	f.AgentFlavor = "spark-light"
	f.RuntimeMode = "direct"
	f.ControllerURL = "https://authorized-new"
	f.EndpointGeneration = 9
	f.AppliedRevision = 12
	f.AppliedHash = "new-applied"
	b, _ := json.Marshal(f)
	os.WriteFile(o.ConfigPath, b, 0600)
	os.WriteFile(filepath.Join(o.StateDir, "bin", "monik-agent"), []byte("new-worker"), 0755)
	os.WriteFile(o.UnitPath, []byte("new-unit"), 0644)
	os.WriteFile(f.CredentialPath, []byte("current-credential"), 0600)
	if err := RollbackLinux(o); err != nil {
		t.Fatal(err)
	}
	s, err := configfile.Load(o.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if s.File.AgentFlavor != "" || s.File.RuntimeMode != "" || s.File.EndpointGeneration != 9 || s.File.ControllerURL != f.ControllerURL || s.File.AppliedRevision != 12 || s.File.AppliedHash != f.AppliedHash {
		t.Fatal("rollback rewound current enrollment or failed mode restoration", s.File)
	}
	w, err := os.ReadFile(filepath.Join(o.StateDir, "bin", "monik-agent"))
	if err != nil || string(w) != "old-worker" {
		t.Fatal("old worker not restored", err)
	}
	c, _ := os.ReadFile(f.CredentialPath)
	if string(c) != "current-credential" {
		t.Fatal("credential changed")
	}
}
func TestLocalRollbackRejectsTamperedOrForeignRecoveryBeforeEffects(t *testing.T) {
	for _, mode := range []string{"tampered", "foreign"} {
		t.Run(mode, func(t *testing.T) {
			o, f := recoveryFixture(t)
			if mode == "tampered" {
				b, _ := os.ReadFile(filepath.Join(o.Prefix, "recovery.json"))
				var r recoveryPoint
				json.Unmarshal(b, &r)
				os.WriteFile(filepath.Join(o.Prefix, "recovery", r.ID, "worker"), []byte("tampered"), 0600)
			} else {
				f.AgentID = "another-agent"
				b, _ := json.Marshal(f)
				os.WriteFile(o.ConfigPath, b, 0600)
			}
			os.WriteFile(filepath.Join(o.StateDir, "bin", "monik-agent"), []byte("new-working"), 0755)
			if err := RollbackLinux(o); err == nil {
				t.Fatal("unsafe rollback accepted")
			}
			b, _ := os.ReadFile(filepath.Join(o.StateDir, "bin", "monik-agent"))
			if string(b) != "new-working" {
				t.Fatal("failed preflight changed working slot")
			}
		})
	}
}

func TestRollbackRejectsRelativePathsWithoutAncestorTraversal(t *testing.T) {
	o := LinuxDefaults()
	o.ConfigPath = "relative/agent.json"
	o.StateDir = "relative"
	if err := RollbackLinux(o); err == nil {
		t.Fatal("relative recovery path accepted")
	}
}

func TestHostOnlyUpgradeGetsFreshRecovery(t *testing.T) {
	o, f := recoveryFixture(t)
	if err := settleRecovery(o, secure.SHA256Bytes([]byte("new-worker")), secure.SHA256Bytes(nil), secure.SHA256Bytes([]byte("new-unit")), "spark-light"); err != nil {
		t.Fatal(err)
	}
	if err := saveRecovery(o, f, []byte("old-worker"), []byte("new-host"), "old-independent-unit", false); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(o.Prefix, "recovery.json"))
	var r recoveryPoint
	json.Unmarshal(raw, &r)
	old, _ := os.ReadFile(filepath.Join(o.Prefix, "recovery", r.ID, "host"))
	if string(old) != "old-host" {
		t.Fatal("host-only upgrade did not preserve host")
	}
}

func TestPartialConversionRetryRetainsPreviousGoodWorker(t *testing.T) {
	o, f := recoveryFixture(t)
	before, _ := os.ReadFile(filepath.Join(o.Prefix, "recovery.json"))
	os.WriteFile(filepath.Join(o.StateDir, "bin", "monik-agent"), []byte("new-worker"), 0755)
	if err := saveRecovery(o, f, []byte("new-worker"), nil, "new-unit", true); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(o.Prefix, "recovery.json"))
	if string(before) != string(after) {
		t.Fatal("retry replaced original recovery point")
	}
	if err := RollbackLinux(o); err != nil {
		t.Fatal(err)
	}
	old, _ := os.ReadFile(filepath.Join(o.StateDir, "bin", "monik-agent"))
	if string(old) != "old-worker" {
		t.Fatal("partial candidate was restored as previous-good")
	}
}
func TestPartialConversionRejectsDifferentCandidate(t *testing.T) {
	o, f := recoveryFixture(t)
	before, _ := os.ReadFile(filepath.Join(o.Prefix, "recovery.json"))
	if err := saveRecovery(o, f, []byte("unrelated-worker"), nil, "new-unit", true); err == nil {
		t.Fatal("different unfinished candidate accepted")
	}
	after, _ := os.ReadFile(filepath.Join(o.Prefix, "recovery.json"))
	if string(before) != string(after) {
		t.Fatal("failed retry changed rollback authority")
	}
}

func TestRollbackKeepsTrustedCLISeparateFromWritableActiveSlot(t *testing.T) {
	o, f := recoveryFixture(t)
	if err := settleRecovery(o, secure.SHA256Bytes([]byte("new-worker")), secure.SHA256Bytes(nil), secure.SHA256Bytes([]byte("new-unit")), "spark-light"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(o.StateDir, "bin", "monik-agent"), []byte("service-owned-active"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := saveRecovery(o, f, []byte("next-worker"), nil, "next-unit", true); err != nil {
		t.Fatal(err)
	}
	if err := RollbackLinux(o); err != nil {
		t.Fatal(err)
	}
	a, _ := os.ReadFile(filepath.Join(o.StateDir, "bin", "monik-agent"))
	p, _ := os.ReadFile(filepath.Join(o.Prefix, "monik-agent"))
	if string(a) != "service-owned-active" || string(p) != "old-worker" {
		t.Fatal("service bytes promoted into root CLI", string(a), string(p))
	}
}
