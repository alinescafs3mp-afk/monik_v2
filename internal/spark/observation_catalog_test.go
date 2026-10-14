package spark

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
)

func catalogFixture(t *testing.T) (Profile, []byte) {
	t.Helper()
	version := "reviewed-version"
	p := Profile{ID: "configured-model", DisplayName: "Configured model", Engine: "vllm", ModelRef: "configured/model", EngineVersion: &version, Settings: []protocol.SparkSetting{{Name: "context_length", Value: 262144, Provenance: "configured"}, {Name: "reasoning_effort", Value: "high", Provenance: "configured"}, {Name: "enable_thinking", Value: true, Provenance: "configured"}, {Name: "chunked_prefill_size", Value: 8192, Provenance: "configured"}, {Name: "dtype", Value: nil, Provenance: "unknown"}}, ObservationOnly: true, AllowedActions: []string{}, Target: Target{Kind: configuredCatalogKind}}
	b, err := json.Marshal(ConfiguredCatalog{SchemaVersion: 1, Profiles: []ConfiguredCatalogEntry{catalogEntry(p)}})
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(b)
	p.Target.CatalogSHA256 = hex.EncodeToString(hash[:])
	p.Target.DefinitionHash, err = configuredCatalogDefinitionFromBytes(p, b)
	if err != nil {
		t.Fatal(err)
	}
	p.Revision = ProfileRevision(p)
	return p, b
}

func TestConfiguredCatalogTypedMetadataAndSeparateApprovalDomain(t *testing.T) {
	p, b := catalogFixture(t)
	first, err := configuredCatalogDefinitionFromBytes(p, b)
	if err != nil || first != p.Target.DefinitionHash {
		t.Fatalf("typed configuration unavailable: %s %v", first, err)
	}
	if first == "sha256:"+p.Target.CatalogSHA256 {
		t.Fatal("configured catalog reused raw source hash as supervisor authority")
	}
	policy := Policy{SchemaVersion: 1, AgentID: "catalog-agent", ControllerID: "catalog-controller", CollectorUID: 1000, StateDir: t.TempDir(), Profiles: []Profile{p}}
	if err := policy.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, enabled := range []bool{false, true} {
		public := p.Public(enabled)
		if public.Supervisor != "unknown" || public.ControlReason != "configured_catalog_runtime_unobserved" || public.ControlAllowed || public.LogAllowed || len(public.AllowedActions) != 0 || public.EndpointHint != nil {
			t.Fatalf("configuration claims authority/runtime: %+v", public)
		}
	}
	if validUnit("spark-v4-model@.service") || validUnit("spark-v4-model@00000000000000000000000000000000.service") {
		t.Fatal("catalog weakened existing exact-unit effect boundary")
	}
}

func TestConfiguredCatalogRejectsMissingDriftAndCrossProfileMetadata(t *testing.T) {
	p, b := catalogFixture(t)
	for _, mode := range []string{"source-bytes", "profile-settings", "model-ref", "missing-profile", "bad-hash", "uppercase-hash"} {
		t.Run(mode, func(t *testing.T) {
			q := p
			input := append([]byte{}, b...)
			switch mode {
			case "source-bytes":
				input = append(input, '\n')
			case "profile-settings":
				q.Settings = append(append([]protocol.SparkSetting{}, p.Settings...), protocol.SparkSetting{Name: "max_concurrency", Value: 8, Provenance: "configured"})
			case "model-ref":
				q.ModelRef = "different/model"
			case "missing-profile":
				q.ID = "missing-model"
			case "bad-hash":
				q.Target.CatalogSHA256 = strings.Repeat("z", 64)
			case "uppercase-hash":
				q.Target.CatalogSHA256 = strings.ToUpper(q.Target.CatalogSHA256)
			}
			if _, err := configuredCatalogDefinitionFromBytes(q, input); err == nil {
				t.Fatalf("catalog accepted stale %s", mode)
			}
		})
	}
}

func TestConfiguredCatalogRejectsSourceInjectionAndRuntimeEvidence(t *testing.T) {
	p, b := catalogFixture(t)
	for _, mode := range []string{"source-path", "selector", "runtime-state", "duplicate-field", "duplicate-id", "bad-schema", "empty-catalog", "null-profiles", "oversize"} {
		t.Run(mode, func(t *testing.T) {
			input := string(b)
			switch mode {
			case "source-path":
				input = strings.Replace(input, "{", `{"source_path":"/etc/shadow",`, 1)
			case "selector":
				input = strings.Replace(input, `"profile_id":`, `"selector":"$(touch /tmp/effect)","profile_id":`, 1)
			case "runtime-state":
				input = strings.Replace(input, `"profile_id":`, `"supervisor_state":"stopped","readiness":"ready","profile_id":`, 1)
			case "duplicate-field":
				input = strings.Replace(input, `"schema_version":1`, `"schema_version":1,"schema_version":1`, 1)
			case "duplicate-id":
				entry, _ := json.Marshal(catalogEntry(p))
				input = `{"schema_version":1,"profiles":[` + string(entry) + "," + string(entry) + "]}"
			case "bad-schema":
				input = strings.Replace(input, `"schema_version":1`, `"schema_version":4`, 1)
			case "empty-catalog":
				input = `{"schema_version":1,"profiles":[]}`
			case "null-profiles":
				input = `{"schema_version":1,"profiles":null}`
			case "oversize":
				input += strings.Repeat(" ", configuredCatalogLimit)
			}
			q := p
			hash := sha256.Sum256([]byte(input))
			q.Target.CatalogSHA256 = hex.EncodeToString(hash[:])
			if _, err := configuredCatalogDefinitionFromBytes(q, []byte(input)); err == nil {
				t.Fatalf("typed source accepted %s", mode)
			}
		})
	}
}

func TestConfiguredCatalogRejectsUnclassifiedSecretAndNonfiniteSettings(t *testing.T) {
	p, _ := catalogFixture(t)
	bad := []protocol.SparkSetting{{Name: "api_key", Value: "secret", Provenance: "configured"}, {Name: "context_length", Value: map[string]any{"path": "/etc/shadow"}, Provenance: "configured"}, {Name: "context_length", Value: math.NaN(), Provenance: "configured"}, {Name: "context_length", Value: math.Inf(1), Provenance: "configured"}, {Name: "context_length", Value: 1e20, Provenance: "configured"}, {Name: "context_length", Value: 0, Provenance: "unknown"}, {Name: "dtype", Value: "auto\nAuthorization: secret", Provenance: "configured"}, {Name: "enable_thinking", Value: "true", Provenance: "configured"}, {Name: "reasoning_effort", Value: "$(touch /tmp/effect)", Provenance: "configured"}, {Name: "chunked_prefill_size", Value: 2.5, Provenance: "configured"}, {Name: "max_concurrency", Value: 1, Provenance: "observed-effective"}, {Name: "dtype", Value: nil, Provenance: "configured"}}
	for n, s := range bad {
		q := p
		q.Settings = []protocol.SparkSetting{s}
		if err := q.validateConfiguredCatalog(); err == nil {
			t.Fatalf("accepted invalid setting %d", n)
		}
	}
	p.Settings = append(p.Settings, p.Settings[0])
	if err := p.validateConfiguredCatalog(); err == nil {
		t.Fatal("accepted duplicate metadata setting")
	}
}

func TestConfiguredCatalogPermanentNoAuthorityEvenMalformedInMemory(t *testing.T) {
	p, _ := catalogFixture(t)
	for _, mode := range []string{"observation-removed", "actions", "logs", "unit", "uid", "root", "trusted", "lock", "docker", "frozen", "health", "weights", "redaction", "port", "memory", "exclusion", "readiness", "stop"} {
		t.Run(mode, func(t *testing.T) {
			q := p
			switch mode {
			case "observation-removed":
				q.ObservationOnly = false
			case "actions":
				q.AllowedActions = []string{"start"}
			case "logs":
				q.LogEnabled = true
			case "unit":
				q.Target.Unit = "spark-sglang.service"
			case "uid":
				q.Target.UserUID = 1000
			case "root":
				q.Target.AllowRootUnit = true
			case "trusted":
				q.Target.TrustedFiles = []string{"/usr/bin/bash"}
			case "lock":
				q.Target.SharedLock = "/run/model.lock"
			case "docker":
				q.Target.DockerSocket, q.Target.ContainerID = "/run/docker.sock", strings.Repeat("a", 64)
			case "frozen":
				q.Target.Frozen = &DockerDeclaration{}
			case "health":
				q.HealthURL, q.HealthScope = "http://127.0.0.1:30000/health", "deployment"
			case "weights":
				q.Weights = []string{"/model/weights"}
			case "redaction":
				q.RedactionFiles = []string{"/etc/secret"}
			case "port":
				q.Port = 30000
			case "memory":
				q.RequiredMemoryBytes = 1
			case "exclusion":
				q.ExclusionGroups = []string{"gpu"}
			case "readiness":
				q.ReadinessSeconds = 1
			case "stop":
				q.StopSeconds = 1
			}
			q.Revision = ProfileRevision(q)
			policy := Policy{SchemaVersion: 1, AgentID: "agent", ControllerID: "controller", CollectorUID: 1000, StateDir: t.TempDir(), Profiles: []Profile{q}}
			if policy.Validate() == nil {
				t.Fatalf("policy granted %s", mode)
			}
			if _, err := (LocalSupervisor{}).Definition(context.Background(), q); err == nil {
				t.Fatalf("definition granted %s", mode)
			}
		})
	}
	p.ObservationOnly, p.LogEnabled, p.AllowedActions = false, true, []string{"start", "stop", "restart"}
	p.HealthURL = "http://127.0.0.1:30000/health"
	public := p.Public(true)
	if public.ControlAllowed || public.LogAllowed || len(public.AllowedActions) != 0 || public.EndpointHint != nil {
		t.Fatal("malformed catalog advertised authority")
	}
	for _, action := range []string{"start", "stop", "restart"} {
		if _, err := (LocalSupervisor{}).Effect(context.Background(), p, action); err == nil {
			t.Fatalf("direct effect admitted %s", action)
		}
	}
	if r, err := (LocalSupervisor{}).Logs(context.Background(), p); r != nil || err == nil {
		t.Fatal("direct logs admitted")
	}
}

func TestConfiguredCatalogProtectedFileSymlinkOwnershipAndReadBudget(t *testing.T) {
	// A private cache fixture avoids world-writable /tmp ancestors; production
	// still has exactly one root-owned /etc source and no configurable path.
	base, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(base, 0700); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(base, "monik-catalog-fs-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	owner := uint32(os.Geteuid())
	file := filepath.Join(dir, "catalog.json")
	_, data := catalogFixture(t)
	if err = os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	if read, err := readConfiguredCatalogFile(file, owner); err != nil || string(read) != string(data) {
		t.Fatalf("protected bounded source: %v", err)
	}
	if _, err := readConfiguredCatalogFile(file, owner+1); err == nil {
		t.Fatal("wrong source owner accepted")
	}
	link := filepath.Join(dir, "source-link")
	if err = os.Symlink(file, link); err != nil {
		t.Fatal(err)
	}
	if _, err := readConfiguredCatalogFile(link, owner); err == nil {
		t.Fatal("source symlink accepted")
	}
	parentLink := filepath.Join(dir, "parent-link")
	if err = os.Symlink(dir, parentLink); err != nil {
		t.Fatal(err)
	}
	if _, err := readConfiguredCatalogFile(filepath.Join(parentLink, "catalog.json"), owner); err == nil {
		t.Fatal("parent symlink accepted")
	}
	if err = os.Chmod(file, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := readConfiguredCatalogFile(file, owner); err == nil {
		t.Fatal("nonprivate source accepted")
	}
	if err = os.Chmod(file, 0600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(dir, "hardlink")
	if err = os.Link(file, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := readConfiguredCatalogFile(file, owner); err == nil {
		t.Fatal("aliased source accepted")
	}
	if err = os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(file, make([]byte, configuredCatalogLimit+1), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readConfiguredCatalogFile(file, owner); err == nil {
		t.Fatal("oversize source accepted")
	}
	if err = os.Chmod(dir, 0777); err != nil {
		t.Fatal(err)
	}
	if _, err := readConfiguredCatalogFile(file, owner); err == nil {
		t.Fatal("writable source ancestor accepted")
	}
}

func TestConfiguredCatalogSnapshotNeverRefreshesRuntimeOrJournal(t *testing.T) {
	for _, state := range []string{"unknown", "running", "starting", "stopped"} {
		t.Run(state, func(t *testing.T) {
			c, f, _ := controlFixture(t)
			p, _ := catalogFixture(t)
			c.Policy.Profiles = []Profile{p}
			old := runtimeProfile{Generation: 42, Identity: "old-instance", State: "running", StartingAt: time.Now().Add(-time.Hour), Reserved: true}
			c.journal.Profiles[p.ID] = old
			f.instances[p.ID] = Instance{State: state, Identity: "not-a-runtime-witness", Available: true}
			for _, enabled := range []bool{false, true} {
				c.Policy.Enabled = enabled
				snapshot, err := c.Snapshot(context.Background())
				if err != nil || !snapshot.Inventory.Complete || len(snapshot.States) != 1 {
					t.Fatalf("snapshot unavailable: %+v %v", snapshot, err)
				}
				s := snapshot.States[0]
				if s.Generation != 0 || s.SupervisorState != "unknown" || s.Readiness != "unknown" || s.Quality != "unknown" || s.Reason != "configured_catalog_runtime_unobserved" {
					t.Fatalf("configuration invented runtime: %+v", s)
				}
				if !reflect.DeepEqual(c.journal.Profiles[p.ID], old) {
					t.Fatal("configured catalog rewrote runtime journal")
				}
			}
			// A malformed in-memory catalog still cannot enter remote/local effects.
			p.ObservationOnly, p.AllowedActions = false, []string{"start", "stop", "restart"}
			p.Revision = ProfileRevision(p)
			c.Policy.Profiles = []Profile{p}
			for _, action := range []string{"start", "stop", "restart"} {
				if got := c.Execute(context.Background(), actionRequest(c, p, "catalog-remote-"+action, action)); got.ErrorCode != "local_action_not_approved" {
					t.Fatalf("remote catalog action admitted: %+v", got)
				}
				if got := c.ExecuteLocal(context.Background(), "catalog-local-"+action, p.ID, action, true); got.ErrorCode != "local_action_not_approved" {
					t.Fatalf("local catalog action admitted: %+v", got)
				}
			}
			if len(f.effects) != 0 || f.logCalls != 0 {
				t.Fatal("catalog reached fake effect/log adapter")
			}
		})
	}
}

type unavailableConfiguredCatalogSupervisor struct{ Supervisor }

func (unavailableConfiguredCatalogSupervisor) Inspect(context.Context, Profile) (Instance, error) {
	return Instance{}, errors.New("configured source unavailable")
}

func TestConfiguredCatalogUnavailableSourceDoesNotClaimLegacyGeneration(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		c, _, _ := controlFixture(t)
		p, _ := catalogFixture(t)
		c.Policy.Profiles = []Profile{p}
		c.Policy.Enabled = enabled
		old := runtimeProfile{Generation: 42, Identity: "legacy-instance", State: "running", Reserved: true}
		c.journal.Profiles[p.ID] = old
		c.supervisor = unavailableConfiguredCatalogSupervisor{c.supervisor}
		got, err := c.Snapshot(context.Background())
		if err != nil || got.Inventory.Complete || len(got.States) != 1 {
			t.Fatalf("unavailable catalog projection: %+v %v", got, err)
		}
		s := got.States[0]
		if s.Generation != 0 || s.SupervisorState != "unknown" || s.Readiness != "unknown" || s.Quality != "error" {
			t.Fatalf("unavailable catalog claimed runtime: %+v", s)
		}
		if !reflect.DeepEqual(c.journal.Profiles[p.ID], old) {
			t.Fatal("unavailable catalog changed the legacy runtime journal")
		}
	}
}
