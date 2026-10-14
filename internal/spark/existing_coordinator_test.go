package spark

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"golang.org/x/sys/unix"
)

type existingFake struct {
	fakeSupervisor
	mu                    sync.Mutex
	snapshot              existingSnapshot
	authority             existingAuthority
	receipts              map[string]existingReceipt
	submitted             []existingRequest
	looked                []existingRequest
	lost                  bool
	omitProof             bool
	staleInstanceOnSubmit bool
	submitGate            chan struct{}
}

func (f *existingFake) Inspect(ctx context.Context, p Profile) (Instance, error) {
	s, e := f.ExistingObserve(ctx, p)
	return s.instance(p), e
}
func (f *existingFake) ExistingObserve(_ context.Context, p Profile) (existingSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.snapshot = existingFakePhysical(f.snapshot, p)
	s := f.snapshot
	s.existingAuthority = f.authority
	s.ObservedAtMS = time.Now().UnixMilli()
	return s, nil
}
func (f *existingFake) ExistingBind(_ context.Context, _ Profile, b existingBinding) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.authority = b.existingAuthority
	return nil
}
func (f *existingFake) ExistingSubmit(ctx context.Context, p Profile, q existingRequest) (existingReceipt, error) {
	f.mu.Lock()
	f.submitted = append(f.submitted, q)
	if f.staleInstanceOnSubmit {
		f.snapshot.InstanceIdentity = strings.Repeat("f", 64)
	}
	s := f.snapshot
	if q.existingAuthority != f.authority || q.ExpectedGeneration != *s.Generation || q.ExpectedInstance != s.InstanceIdentity || q.ProfileRevision != s.ProfileRevision {
		f.mu.Unlock()
		return existingReceipt{}, fmt.Errorf("stale backend")
	}
	if q.Action == "start" {
		g := *s.Generation + 1
		s.Generation = &g
		run := strings.TrimPrefix(digest(q.OperationID), "sha256:")[:32]
		s.RunID = &run
		attempt := strings.TrimPrefix(digest([]any{q.OperationID, g}), "sha256:")[:32]
		s.Attempt = &attempt
		s.State = "STARTING"
		s.ProcessActive = true
		s.APIReady = false
		s.Instance = &existingPhysicalInstance{RunID: &run}
		s.ShutdownVerified = false
		s.ShutdownWitness = nil
	} else {
		s.State = "STOPPED"
		s.ProcessActive = false
		s.APIReady = false
		s.ShutdownVerified = !f.omitProof
	}
	s = existingFakePhysical(s, p)
	f.snapshot = s
	result := s.existingResult

	phase := "completed"
	if q.Action == "start" {
		phase = "dispatched"
	}
	r := existingReceipt{OperationID: q.OperationID, ProfileID: p.ID, Action: q.Action, Phase: phase, RunID: s.RunID, Generation: *s.Generation, Attempt: s.Attempt, Result: &result}
	f.receipts[q.OperationID] = r
	lost := f.lost
	gate := f.submitGate
	f.mu.Unlock()
	if gate != nil && q.Action == "start" {
		select {
		case <-gate:
		case <-ctx.Done():
			return existingReceipt{}, ctx.Err()
		}
	}
	if lost {
		return existingReceipt{}, fmt.Errorf("lost reply")
	}
	return r, nil
}
func (f *existingFake) ExistingReceipt(_ context.Context, _ Profile, q existingRequest, a existingAuthority) (existingReceipt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.looked = append(f.looked, q)
	if a != f.authority {
		return existingReceipt{}, fmt.Errorf("authority")
	}
	r, ok := f.receipts[q.OperationID]
	if !ok {
		return r, fmt.Errorf("missing")
	}
	return r, nil
}
func existingFakePhysical(s existingSnapshot, p Profile) existingSnapshot {
	if s.RunID != nil {
		if s.Attempt == nil {
			attempt := strings.Repeat("5", 32)
			s.Attempt = &attempt
		}
		if s.Instance == nil {
			s.Instance = &existingPhysicalInstance{RunID: s.RunID}
		}
		physical := *s.Instance
		physical.RunID = s.RunID
		if physical.Owner == nil {
			physical.Owner = &existingOwner{Unit: "spark-v4-" + p.ID + "@" + *s.RunID + ".service", InvocationID: strings.Repeat("6", 32), PID: 42, Birth: "100"}
		}
		if physical.ContainerID == nil {
			cid := strings.Repeat("7", 64)
			physical.ContainerID = &cid
		}
		if physical.ContainerCreated == nil {
			created := "2026-10-14T20:00:00Z"
			physical.ContainerCreated = &created
		}
		if physical.EnginePID == nil {
			pid := 43
			physical.EnginePID = &pid
		}
		if physical.EngineBirth == nil {
			birth := "101"
			physical.EngineBirth = &birth
		}
		s.Instance = &physical
		s.InstanceIdentity = existingPhysicalIdentity(s.Instance)
	} else {
		s.Instance = nil
		s.InstanceIdentity = ""
		s.Attempt = nil
	}
	if s.ShutdownVerified {
		witness := &existingShutdownWitness{Kind: "no_owned_run", ModelID: p.ID, RunID: s.RunID, Generation: valueGeneration(s.Generation), Attempt: s.Attempt, InstanceIdentity: s.InstanceIdentity}
		if s.Instance != nil {
			witness.Kind = "exact_run_resources_released"
			witness.Owner = s.Instance.Owner
			witness.ContainerID = s.Instance.ContainerID
			witness.ContainerCreated = s.Instance.ContainerCreated
			witness.EnginePID = s.Instance.EnginePID
			witness.EngineBirth = s.Instance.EngineBirth
		}
		s.ShutdownWitness = witness
	} else {
		s.ShutdownWitness = nil
	}
	return s
}
func existingFixture(t *testing.T) (*Coordinator, *existingFake, Profile) {
	t.Helper()
	a := &ExistingCoordinatorApproval{PythonPath: "/usr/bin/python3.14", BackendRevision: strings.Repeat("a", 64)}
	p := Profile{ID: existingIDs[0], Engine: "custom", DisplayName: "real existing controller", Settings: []protocol.SparkSetting{}, AllowedActions: []string{"start", "stop", "restart"}, Target: Target{Kind: existingCoordinatorKind, Existing: a}}
	for _, path := range existingRequiredPaths(p) {
		a.CodePins = append(a.CodePins, ExistingCodePin{path, strings.Repeat("b", 64)})
	}
	p.Target.DefinitionHash = digest(a)
	p.Revision = ProfileRevision(p)
	g := int64(7)
	f := &existingFake{snapshot: existingSnapshot{existingResult: existingResult{ModelID: p.ID, State: "STOPPED", Generation: &g}, ProfileRevision: a.BackendRevision}, receipts: map[string]existingReceipt{}}
	policy := &Policy{SchemaVersion: 1, Enabled: true, AgentID: "fixed-agent", ControllerID: "fixed-controller", CollectorUID: 1000, StateDir: t.TempDir(), Profiles: []Profile{p}}
	if e := policy.Validate(); e != nil {
		t.Fatal(e)
	}
	c, e := OpenCoordinator(policy, f)
	if e != nil {
		t.Fatal(e)
	}
	if e = c.Bind(policy.AgentID, policy.ControllerID, "session-a"); e != nil {
		t.Fatal(e)
	}
	if _, e = c.Snapshot(context.Background()); e != nil {
		t.Fatal(e)
	}
	return c, f, p
}
func TestExistingRevisionGenerationAndNoSecondReservation(t *testing.T) {
	c, f, p := existingFixture(t)
	r := actionRequest(c, p, "job-start", "start")
	got := c.Execute(context.Background(), r)
	if got.Status != protocol.TargetRunning {
		t.Fatalf("%+v", got)
	}
	q := f.submitted[0]
	if q.OperationID != r.JobID || q.ProfileRevision == p.Revision || q.ProfileRevision != p.Target.Existing.BackendRevision || q.ExpectedGeneration != 7 || q.ExpectedInstance != "" {
		t.Fatalf("mapping %+v", q)
	}
	if c.journal.Profiles[p.ID].Reserved {
		t.Fatal("duplicate reservation")
	}
	if f.effects[p.ID+":start"] != 0 {
		t.Fatal("generic supervisor effect used")
	}
	original := q
	f.mu.Lock()
	f.snapshot.InstanceIdentity = strings.Repeat("3", 64)
	f.snapshot.State = "READY"
	f.snapshot.APIReady = true
	rec := f.receipts[q.OperationID]
	rec.Phase = "completed"
	result := f.snapshot.existingResult
	rec.Result = &result
	f.receipts[q.OperationID] = rec
	f.mu.Unlock()
	_, _ = c.Snapshot(context.Background())
	if c.journal.Profiles[p.ID].Generation == 8 {
		t.Fatal("borrowed backend generation")
	}
	got = c.Execute(context.Background(), r)
	if got.Status != protocol.TargetSucceeded {
		t.Fatalf("%+v", got)
	}
	if digest(f.looked[0]) != digest(original) || len(f.submitted) != 1 {
		t.Fatal("immutable request changed/replayed")
	}
}
func TestExistingLostReplyReconnectNeverResends(t *testing.T) {
	c, f, p := existingFixture(t)
	f.lost = true
	r := actionRequest(c, p, "lost-job", "start")
	got := c.Execute(context.Background(), r)
	if got.Status != protocol.TargetUnknownResult {
		t.Fatal(got)
	}
	reopened, e := OpenCoordinator(c.Policy, f)
	if e != nil {
		t.Fatal(e)
	}
	if e = reopened.Bind(c.Policy.AgentID, c.Policy.ControllerID, "session-b"); e != nil {
		t.Fatal(e)
	}
	got = reopened.Execute(context.Background(), r)
	if got.Status != protocol.TargetRunning || len(f.submitted) != 1 || f.looked[0].AgentSession != "session-a" || f.authority.AgentSession != "session-b" {
		t.Fatalf("reconnect %+v %+v", got, f.looked)
	}
	changed := r
	changed.Deadline = changed.Deadline.Add(time.Second)
	if got = reopened.Execute(context.Background(), changed); got.ErrorCode != "idempotency_conflict" {
		t.Fatal(got)
	}
}
func TestExistingAdmissionFences(t *testing.T) {
	for _, test := range []string{"revision", "backendrevision", "generation", "session", "epoch", "boot", "lease", "confirmation"} {
		t.Run(test, func(t *testing.T) {
			c, f, p := existingFixture(t)
			r := actionRequest(c, p, "denied", "start")
			switch test {
			case "revision":
				r.Params.ProfileRevision = "bad"
			case "backendrevision":
				f.snapshot.ProfileRevision = strings.Repeat("c", 64)
			case "generation":
				r.Params.ExpectedGeneration++
			case "session":
				r.Params.ExpectedAgentSession = "old"
			case "epoch":
				r.Params.ControlEpoch = "old"
			case "boot":
				f.authority.BootID = "old"
			case "lease":
				r.Deadline = time.Now().Add(-time.Second)
			case "confirmation":
				f.snapshot.State = "READY"
				f.snapshot.ProcessActive = true
				r.Action = "model.stop"
				r.Params.ConfirmDisruption = false
			}
			got := c.Execute(context.Background(), r)
			if got.Status != protocol.TargetRejected || len(f.submitted) > 0 {
				t.Fatalf("%+v", got)
			}
		})
	}
}
func TestExistingReceiptNeverBorrowsReplacementOrInactiveLabel(t *testing.T) {
	c, f, p := existingFixture(t)
	r := actionRequest(c, p, "first", "start")
	_ = c.Execute(context.Background(), r)
	rec := f.receipts[r.JobID]
	rec.Phase = "unresolved"
	rec.Result = &existingResult{Reason: "original_runtime_replaced_or_missing"}
	f.receipts[r.JobID] = rec
	f.snapshot.State = "READY"
	f.snapshot.APIReady = true
	newrun := strings.Repeat("4", 32)
	f.snapshot.RunID = &newrun
	f.snapshot.Instance.RunID = &newrun
	if got := c.Execute(context.Background(), r); got.Status == protocol.TargetSucceeded {
		t.Fatal("adopted replacement")
	}
	_, _ = c.Snapshot(context.Background())
	stop := actionRequest(c, p, "stop", "stop")
	got := c.Execute(context.Background(), stop)
	if got.Status != protocol.TargetSucceeded {
		t.Fatalf("stop %+v", got)
	}
	rec = f.receipts[stop.JobID]
	rec.Result.ShutdownVerified = false
	f.receipts[stop.JobID] = rec
	if got = c.Execute(context.Background(), stop); got.Status != protocol.TargetSucceeded {
		t.Fatal("changed historical completed outcome")
	}
	f.omitProof = true
	unproved := actionRequest(c, p, "unproved-stop", "stop")
	if got = c.Execute(context.Background(), unproved); got.Status == protocol.TargetSucceeded {
		t.Fatal("inactive label certified shutdown")
	}
}
func TestExistingSourceCustodyRejectsWrongBytesOwnerSymlinkAndSealsReplacement(t *testing.T) {
	userHome, e := os.UserHomeDir()
	if e != nil {
		t.Fatal(e)
	}
	dir, e := os.MkdirTemp(userHome, ".monik-custody-")
	if e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "source.py")
	raw := []byte("print('fixed')\n")
	if e = os.WriteFile(path, raw, 0600); e != nil {
		t.Fatal(e)
	}
	h := sha256.Sum256(raw)
	pin := ExistingCodePin{path, hex.EncodeToString(h[:])}
	if _, e = existingPinnedFile(pin, 0, false); e == nil {
		t.Fatal("wrong owner")
	}
	bad := pin
	bad.SHA256 = strings.Repeat("a", 64)
	if _, e = existingPinnedFile(bad, uint32(os.Getuid()), false); e == nil {
		t.Fatal("wrong bytes")
	}
	f, e := existingPinnedFile(pin, uint32(os.Getuid()), false)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	if _, e = f.WriteAt([]byte("bad"), 0); e == nil {
		t.Fatal("unsealed bytes")
	}
	replacement := filepath.Join(dir, "replacement")
	_ = os.WriteFile(replacement, []byte("replacement"), 0600)
	_ = os.Rename(replacement, path)
	b := make([]byte, len(raw))
	_, _ = f.ReadAt(b, 0)
	if string(b) != string(raw) {
		t.Fatal("custody followed replacement")
	}
	link := filepath.Join(dir, "link")
	_ = os.Symlink(path, link)
	pin.Path = link
	if _, e = existingPinnedFile(pin, uint32(os.Getuid()), false); e == nil {
		t.Fatal("symlink")
	}
	seals, e := unix.FcntlInt(f.Fd(), unix.F_GET_SEALS, 0)
	if e != nil || seals&unix.F_SEAL_WRITE == 0 {
		t.Fatal("seal missing")
	}
}
func TestExistingStrictForeignDuplicateSchemaAndLogDenial(t *testing.T) {
	_, _, p := existingFixture(t)
	q := existingRequest{OperationID: "j", ProfileID: p.ID, Action: "start"}
	for _, raw := range []string{`{"schema_version":1,"schema_version":1}`, `{"schema_version":1,"exec":"bad"}`, `{"schema_version":"1"}`} {
		var response existingResponse
		if strictDecode([]byte(raw), &response) == nil {
			t.Fatal("accepted", raw)
		}
	}
	for _, r := range []existingReceipt{{OperationID: "foreign", ProfileID: p.ID, Action: "start", Phase: "completed"}, {OperationID: "j", ProfileID: "foreign", Action: "start", Phase: "completed"}, {OperationID: "j", ProfileID: p.ID, Action: "stop", Phase: "completed"}} {
		if validateExistingReceipt(r, q) == nil {
			t.Fatal("foreign receipt")
		}
	}
	if _, e := (LocalSupervisor{}).Logs(context.Background(), p); e == nil {
		t.Fatal("unsafe logs")
	}
	if _, e := (LocalSupervisor{}).Effect(context.Background(), p, "start"); e == nil {
		t.Fatal("requestless effect")
	}
	p.Target.SharedLock = "/var/lib/spark-model-collection-v4/collection.lock"
	if p.validateExistingCoordinator() == nil {
		t.Fatal("competing lock")
	}
	b, _ := json.Marshal(p.Public(true))
	if strings.Contains(string(b), "code_pins") || strings.Contains(string(b), "python_path") {
		t.Fatal("local approval leaked")
	}
}

func TestExistingRestartHasDistinctFreshAuthorizedStages(t *testing.T) {
	c, f, p := existingFixture(t)
	g := int64(8)
	run := strings.Repeat("8", 32)
	f.snapshot.Generation = &g
	f.snapshot.RunID = &run
	f.snapshot.InstanceIdentity = strings.Repeat("8", 64)
	f.snapshot.Instance = &existingPhysicalInstance{RunID: &run}
	f.snapshot.State = "READY"
	f.snapshot.ProcessActive = true
	f.snapshot.APIReady = true
	_, _ = c.Snapshot(context.Background())
	stop := actionRequest(c, p, "restart-stop", "restart")
	got := c.Execute(context.Background(), stop)
	if got.Stage != "restart_stopped" || got.Status != protocol.TargetAwaitingConfirmation {
		t.Fatalf("%+v", got)
	}
	_, _ = c.Snapshot(context.Background())
	start := actionRequest(c, p, "restart-start", "start")
	start.OperationID = stop.OperationID
	start.ParentJobID = stop.JobID
	got = c.Execute(context.Background(), start)
	if got.Status != protocol.TargetRunning {
		t.Fatalf("%+v", got)
	}
	if len(f.submitted) != 2 || f.submitted[0].OperationID == f.submitted[1].OperationID || f.submitted[1].ParentOperationID != stop.JobID {
		t.Fatalf("stage mapping %+v", f.submitted)
	}
	bad := start
	bad.JobID = "foreign-stage"
	bad.ParentJobID = "foreign-parent"
	if got = c.Execute(context.Background(), bad); got.Status != protocol.TargetRejected {
		t.Fatal("unauthorized stage")
	}
}
func TestExistingStopRemainsAvailableDuringStartup(t *testing.T) {
	c, f, p := existingFixture(t)
	gate := make(chan struct{})
	f.submitGate = gate
	r := actionRequest(c, p, "starting", "start")
	done := make(chan protocol.JobReceipt, 1)
	go func() { done <- c.Execute(context.Background(), r) }()
	deadline := time.Now().Add(time.Second)
	for {
		f.mu.Lock()
		ready := len(f.submitted) == 1
		f.mu.Unlock()
		if ready {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("start did not dispatch")
		}
		time.Sleep(time.Millisecond)
	}
	_, _ = c.Snapshot(context.Background())
	stop := actionRequest(c, p, "cancel-start", "stop")
	got := c.Execute(context.Background(), stop)
	if got.Status != protocol.TargetSucceeded {
		t.Fatal(got)
	}
	close(gate)
	select {
	case got = <-done:
		if got.ErrorCode != "superseded_by_stop" || got.Status != protocol.TargetFailed {
			t.Fatalf("start lacks physical stop closure: %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("startup blocked stop")
	}
}

func TestExistingPinnedInterpreterTransportRealLocalFakeController(t *testing.T) {
	python, e := filepath.EvalSymlinks("/usr/bin/python3")
	if e != nil {
		t.Skip("fixed local Python unavailable")
	}
	binary, e := os.ReadFile(python)
	if e != nil {
		t.Fatal(e)
	}
	hash := sha256.Sum256(binary)
	interpreter, e := existingPinnedFile(ExistingCodePin{python, hex.EncodeToString(hash[:])}, 0, true)
	if e != nil {
		t.Fatal(e)
	}
	defer interpreter.Close()
	// The process remains kernel-nonroot. This test-only fake geteuid result
	// exercises the production bootstrap/FD contract without granting root effects.
	bridge := []byte(`import json

def strict_object(pairs):
 value={}
 for key,item in pairs:
  if key in value:raise ValueError('duplicate')
  value[key]=item
 return value

def decode_envelope(raw):
 value=json.loads(raw,object_pairs_hook=strict_object)
 if set(value)!={'schema_version','method','profile_ids'} or value['schema_version']!=1 or value['method']!='observe_many':raise ValueError('schema')
 return value

def dispatch(controller,body):
 return {'schema_version':1,'models':[controller.bridge_snapshot(mid) for mid in body['profile_ids']]}
`)
	model := []byte(`Refusal=RuntimeError
ObservationError=RuntimeError
protect=lambda path:None
unit_name=lambda *args:'fixed-unit'
proc_birth=lambda *args:'fixed-birth'
PROJECT='/opt/spark/model-collection-v4'
MANIFEST=PROJECT+'/config/controller-manifest.json'
def validate_manifest(value):
 assert _LEGACY_SUPPORT.SEAL=='sealed-legacy-source'
 assert _LEGACY_SUPPORT.Refusal is Refusal
 return value
class SystemAdapter:
 def __init__(self,value):pass
class Controller:
 def __init__(self,manifest,adapter):self.manifest=manifest
 def bridge_snapshot(self,mid):
  result=dict(self.manifest);result['model_id']=mid
  return result
`)
	_, _, p := existingFixture(t)
	now := time.Now().UnixMilli()
	snap := map[string]any{"model_id": p.ID, "state": "STOPPED", "process_active": false, "api_ready": false, "context": nil, "endpoint": "http://127.0.0.1:31001", "engine_log": "fixed-private", "action_log": "fixed-private", "message": "fake controller only", "generation": int64(7), "attempt": nil, "run_id": nil, "profile_revision": p.Target.Existing.BackendRevision, "instance_identity": "", "observed_at_ms": now, "instance": nil}
	manifest, _ := json.Marshal(snap)
	paths, _ := json.Marshal(existingRequiredPaths(p))
	files := []*os.File{interpreter}
	for _, raw := range [][]byte{bridge, model, []byte("reserved supervisor"), manifest, []byte("reserved helper"), []byte("reserved unit"), []byte("SEAL='sealed-legacy-source'"), []byte("reserved independent legacy entry"), paths} {
		f, err := existingSealedBytes(raw, false)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
		defer f.Close()
	}
	env := existingEnvelope{SchemaVersion: 1, Method: "observe_many", ProfileIDs: []string{p.ID}}
	input, _ := json.Marshal(env)
	run := func(fakeRoot bool, input []byte) ([]byte, error) {
		for _, f := range files {
			_, _ = f.Seek(0, 0)
		}
		code := existingBootstrap
		if fakeRoot {
			code = "import os;os.geteuid=lambda:0\n" + code
		}
		cmd := exec.Command("/proc/self/fd/3", "-I", "-S", "-B", "-c", code, fmt.Sprint(3+len(p.Target.Existing.CodePins)))
		cmd.ExtraFiles = files
		cmd.Dir = "/"
		cmd.Env = []string{"PATH=/usr/bin:/bin", "PYTHONPATH=/nonexistent/malicious"}
		cmd.Stdin = bytes.NewReader(input)
		return cmd.Output()
	}
	if raw, err := run(false, input); err == nil || !bytes.Contains(raw, []byte("typed_bridge_valueerror")) {
		t.Fatalf("root guard %s %v", raw, err)
	}
	raw, e := run(true, input)
	if e != nil {
		t.Fatalf("fixed FD bootstrap %s %v", raw, e)
	}
	var result existingResponse
	if e = decodeExistingResponse(raw, env, p, &result); e != nil {
		t.Fatalf("strict response %s %v", raw, e)
	}
	if result.Models[0].ModelID != p.ID {
		t.Fatal("foreign result")
	}
	// The same production sealed descriptor bootstrap returns all seven models
	// from one Python process, with no path imports or per-profile executables.
	_, _, profiles := existingRoundFixture(t)
	ids := make([]string, len(profiles))
	for index, profile := range profiles {
		ids[index] = profile.ID
	}
	many := existingEnvelope{SchemaVersion: 1, Method: "observe_many", ProfileIDs: ids}
	manyInput, _ := json.Marshal(many)
	manyRaw, err := run(true, manyInput)
	if err != nil || decodeExistingProfilesResponse(manyRaw, many, profiles, &result) != nil || len(result.Models) != 7 {
		t.Fatalf("one sealed helper seven-profile response: %s %v", manyRaw, err)
	}
	for _, bad := range [][]byte{[]byte(`{"schema_version":1,"schema_version":1,"method":"observe_many","profile_ids":[]}`), []byte(`{"schema_version":1,"method":"observe_many","profile_ids":[],"argv":"bad"}`)} {
		if raw, e = run(true, bad); e == nil || !bytes.Contains(raw, []byte("typed_bridge_valueerror")) {
			t.Fatalf("strict transport accepted %s %v", raw, e)
		}
	}
	badraw := bytes.Replace(raw, []byte("typed_bridge_valueerror"), []byte("typed_bridge_valueerror"), 1)
	if decodeExistingResponse(badraw, env, p, &result) == nil {
		t.Fatal("error envelope accepted")
	}
}

func TestExistingPhysicalProofCannotBeForgedByLabelsOrForeignIdentity(t *testing.T) {
	_, _, p := existingFixture(t)
	run := strings.Repeat("a", 32)
	g := int64(9)
	result := existingResult{ModelID: p.ID, State: "FAILED", Generation: &g, RunID: &run, ShutdownVerified: true}
	w := &existingShutdownWitness{Kind: "exact_run_resources_released", ModelID: p.ID, RunID: &run, Generation: g}
	w.InstanceIdentity = strings.TrimPrefix(digest(map[string]any{"run_id": &run, "owner": nil, "container_id": nil, "container_created": nil, "engine_pid": nil, "engine_birth": nil}), "sha256:")
	result.ShutdownWitness = w
	result.InstanceIdentity = w.InstanceIdentity
	if !validExistingShutdown(result, p.ID, &run, g) {
		t.Fatal("exact failed proof rejected")
	}
	for _, test := range []string{"model", "generation", "instance", "active", "kind"} {
		copyResult := result
		copyWitness := *w
		copyResult.ShutdownWitness = &copyWitness
		switch test {
		case "model":
			copyWitness.ModelID = "foreign"
		case "generation":
			copyWitness.Generation++
		case "instance":
			copyWitness.InstanceIdentity = strings.Repeat("b", 64)
			copyResult.InstanceIdentity = copyWitness.InstanceIdentity
		case "active":
			copyResult.ProcessActive = true
		case "kind":
			copyWitness.Kind = "no_owned_run"
		}
		if validExistingShutdown(copyResult, p.ID, &run, g) {
			t.Fatal("accepted forged " + test)
		}
	}
}

func TestExistingBackendRechecksInstanceAfterFreshObservation(t *testing.T) {
	c, f, p := existingFixture(t)
	f.staleInstanceOnSubmit = true
	r := actionRequest(c, p, "instance-race", "start")
	got := c.Execute(context.Background(), r)
	if got.Status != protocol.TargetUnknownResult || len(f.receipts) != 0 {
		t.Fatalf("stale instance effect %+v", got)
	}
	_ = c.Execute(context.Background(), r)
	if len(f.submitted) != 1 {
		t.Fatal("stale ambiguous request replayed")
	}
}
