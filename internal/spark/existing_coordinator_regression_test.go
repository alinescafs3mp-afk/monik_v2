package spark

import (
	"context"
	"encoding/json"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"os"
	"strings"
	"testing"
	"time"
)

func TestExistingReviewedFinalProofMatchesSubsequentReport(t *testing.T) {
	c, f, p := existingFixture(t)
	r := actionRequest(c, p, "review-start", "start")
	progress := c.Execute(context.Background(), r)
	b, _ := json.Marshal(progress)
	f.mu.Lock()
	rec := f.receipts[r.JobID]
	rec.Phase = "completed"
	result := *rec.Result
	result.State = "READY"
	result.ProcessActive = true
	result.APIReady = true
	rec.Result = &result
	f.receipts[r.JobID] = rec
	f.snapshot.existingResult = result
	f.mu.Unlock()
	got := c.Execute(context.Background(), r)
	if got.Status != protocol.TargetSucceeded {
		t.Fatalf("not completed: %+v", got)
	}
	b, _ = json.Marshal(got)
	report, e := c.Snapshot(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	t.Logf("receipt=%s, next_report_generation=%d, next_instance=%s", b, report.States[0].Generation, f.snapshot.instance(p).Identity)
	if got.Evidence["generation"] != report.States[0].Generation {
		t.Errorf("final receipt retains pre-start Go generation, successor report cannot prove it")
	}
	if got.Evidence["instance_identity"] != f.snapshot.instance(p).Identity {
		t.Errorf("final receipt retains pre-start instance instead of exact started instance")
	}
}

func TestExistingReviewedRestartParentCannotReopen(t *testing.T) {
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
	stop := actionRequest(c, p, "review-restart-stop", "restart")
	if got := c.Execute(context.Background(), stop); got.Stage != "restart_stopped" {
		t.Fatal(got)
	}
	_, _ = c.Snapshot(context.Background())
	start := actionRequest(c, p, "review-restart-start", "start")
	start.OperationID = stop.OperationID
	start.ParentJobID = stop.JobID
	if got := c.Execute(context.Background(), start); got.Status != protocol.TargetRunning {
		t.Fatal(got)
	}
	if c.journal.Attempts[stop.JobID].Stage != "start_attempted" {
		t.Fatal("no pending child")
	}
	_ = c.Execute(context.Background(), stop)
	if got := c.journal.Attempts[stop.JobID].Stage; got != "start_attempted" {
		t.Errorf("immutable parent duplicate regressed stage to %s", got)
	}
}

type reviewForeignAuthorityTransport struct{ *existingFake }

func (f reviewForeignAuthorityTransport) ExistingObserve(ctx context.Context, p Profile) (existingSnapshot, error) {
	s, e := f.existingFake.ExistingObserve(ctx, p)
	s.ControlEpoch = "foreign-epoch"
	s.AgentSession = "foreign-session"
	s.BootID = "foreign-boot"
	s.State = "READY"
	s.ProcessActive = true
	s.APIReady = true
	return s, e
}
func TestExistingReviewedReadyCannotBorrowForeignAuthority(t *testing.T) {
	c, f, p := existingFixture(t)
	r := actionRequest(c, p, "review-authority", "start")
	_ = c.Execute(context.Background(), r)
	f.mu.Lock()
	rec := f.receipts[r.JobID]
	rec.Phase = "completed"
	f.receipts[r.JobID] = rec
	f.mu.Unlock()
	c.supervisor = reviewForeignAuthorityTransport{f}
	if got := c.Execute(context.Background(), r); got.Status == protocol.TargetSucceeded {
		t.Errorf("READY from foreign authority merged into original receipt: %+v", got)
	}
}

func TestExistingReviewedStrictCompletedResultCannotOmitIdentity(t *testing.T) {
	_, _, p := existingFixture(t)
	q := existingRequest{OperationID: "j", ProfileID: p.ID, Action: "start", ExpectedGeneration: 7}
	env := existingEnvelope{SchemaVersion: 1, Method: "submit", Request: &q}
	raw := []byte(`{"schema_version":1,"receipt":{"operation_id":"j","profile_id":"` + p.ID + `","action":"start","phase":"completed","run_id":null,"generation":7,"result":{"state":"READY","process_active":true,"api_ready":true}}}`)
	var result existingResponse
	if decodeExistingResponse(raw, env, p, &result) == nil {
		t.Error("accepted completed READY result missing model_id/run/generation/attempt/context and all other required snapshot fields")
	}
}

func TestExistingRestartParentDuplicateTracksOnlyItsChild(t *testing.T) {
	c, f, p := existingFixture(t)
	run := strings.Repeat("8", 32)
	g := int64(8)
	f.snapshot.Generation = &g
	f.snapshot.RunID = &run
	f.snapshot.Instance = &existingPhysicalInstance{RunID: &run}
	f.snapshot.State = "READY"
	f.snapshot.ProcessActive = true
	f.snapshot.APIReady = true
	_, _ = c.Snapshot(context.Background())
	parent := actionRequest(c, p, "monotonic-parent", "restart")
	if got := c.Execute(context.Background(), parent); got.Stage != "restart_stopped" {
		t.Fatal(got)
	}
	_, _ = c.Snapshot(context.Background())
	child := actionRequest(c, p, "monotonic-child", "start")
	child.OperationID = parent.OperationID
	child.ParentJobID = parent.JobID
	if got := c.Execute(context.Background(), child); got.Status != protocol.TargetRunning {
		t.Fatal(got)
	}
	for count := 0; count < 2; count++ {
		got := c.Execute(context.Background(), parent)
		if got.Status != protocol.TargetRunning || got.Stage != "restart_start_attempted" || c.journal.Attempts[parent.JobID].Stage != "start_attempted" {
			t.Fatalf("parent regressed %+v", got)
		}
	}
	f.mu.Lock()
	f.snapshot.State = "READY"
	f.snapshot.APIReady = true
	receipt := f.receipts[child.JobID]
	receipt.Phase = "completed"
	result := f.snapshot.existingResult
	receipt.Result = &result
	f.receipts[child.JobID] = receipt
	f.mu.Unlock()
	completed := c.Execute(context.Background(), parent)
	if completed.Status != protocol.TargetSucceeded || completed.Stage != "model_restarted" || completed.Evidence["stage_id"] != child.JobID {
		t.Fatal(completed)
	}
	again := c.Execute(context.Background(), parent)
	if digest(completed) != digest(again) || c.journal.Attempts[parent.JobID].Stage != "done" || len(f.submitted) != 2 {
		t.Fatal("terminal parent/child proof changed or replayed")
	}
}

type existingProofMutator struct {
	*existingFake
	coordinator *Coordinator
	mutation    string
}

func (f existingProofMutator) ExistingObserve(ctx context.Context, p Profile) (existingSnapshot, error) {
	s, e := f.existingFake.ExistingObserve(ctx, p)
	s.State = "READY"
	s.ProcessActive = true
	s.APIReady = true
	switch f.mutation {
	case "epoch":
		s.ControlEpoch = "foreign"
	case "session":
		s.AgentSession = "foreign"
	case "boot":
		s.BootID = "foreign"
	case "revision":
		s.ProfileRevision = strings.Repeat("f", 64)
	case "attempt":
		value := strings.Repeat("f", 32)
		s.Attempt = &value
	case "run":
		value := strings.Repeat("f", 32)
		s.RunID = &value
		physical := *s.Instance
		physical.RunID = &value
		s.Instance = &physical
		s.InstanceIdentity = existingPhysicalIdentity(s.Instance)
	case "hash":
		s.InstanceIdentity = strings.Repeat("f", 64)
	case "owner", "container", "pid":
		physical := *s.Instance
		if f.mutation == "owner" {
			owner := *physical.Owner
			owner.PID = 999
			owner.Birth = "foreign"
			physical.Owner = &owner
		}
		if f.mutation == "container" {
			value := strings.Repeat("f", 64)
			physical.ContainerID = &value
		}
		if f.mutation == "pid" {
			value := 999
			physical.EnginePID = &value
		}
		s.Instance = &physical
		s.InstanceIdentity = existingPhysicalIdentity(s.Instance)
	case "coordinator":
		f.coordinator.mu.Lock()
		f.coordinator.session = "replaced-while-observing"
		f.coordinator.mu.Unlock()
	}
	return s, e
}
func TestExistingFinalReadyRechecksAllCurrentAuthorityAndPhysicalBindings(t *testing.T) {
	for _, mutation := range []string{"epoch", "session", "boot", "revision", "attempt", "run", "hash", "owner", "container", "pid", "coordinator"} {
		t.Run(mutation, func(t *testing.T) {
			c, f, p := existingFixture(t)
			request := actionRequest(c, p, "bound-proof", "start")
			_ = c.Execute(context.Background(), request)
			f.mu.Lock()
			receipt := f.receipts[request.JobID]
			receipt.Phase = "completed"
			f.receipts[request.JobID] = receipt
			f.mu.Unlock()
			c.supervisor = existingProofMutator{f, c, mutation}
			got := c.Execute(context.Background(), request)
			if got.Status == protocol.TargetSucceeded || len(f.submitted) != 1 {
				t.Fatalf("foreign/replaced final proof accepted %+v", got)
			}
		})
	}
}

func TestExistingRuntimeResultSchemasRequireEachFieldAndRejectShapeConfusion(t *testing.T) {
	c, f, p := existingFixture(t)
	request := actionRequest(c, p, "strict-runtime", "start")
	_ = c.Execute(context.Background(), request)
	q := *c.journal.Attempts[request.JobID].BackendRequest
	f.mu.Lock()
	receipt := f.receipts[request.JobID]
	f.mu.Unlock()
	result := map[string]any{"model_id": p.ID, "state": "READY", "process_active": true, "api_ready": true, "context": nil, "endpoint": "http://127.0.0.1:31001", "engine_log": "fixed-engine", "action_log": "fixed-action", "message": "bounded", "generation": receipt.Generation, "attempt": receipt.Attempt, "run_id": receipt.RunID}
	encode := func(value map[string]any) []byte {
		raw, _ := json.Marshal(map[string]any{"schema_version": 1, "receipt": map[string]any{"operation_id": q.OperationID, "profile_id": p.ID, "action": "start", "phase": "completed", "run_id": receipt.RunID, "generation": receipt.Generation, "attempt": receipt.Attempt, "result": value}})
		return raw
	}
	envelope := existingEnvelope{SchemaVersion: 1, Method: "receipt", Request: &q, Authority: &f.authority}
	var decoded existingResponse
	if err := decodeExistingResponse(encode(result), envelope, p, &decoded); err != nil {
		t.Fatal("valid full runtime schema", err)
	}
	for field := range result {
		t.Run("missing_"+field, func(t *testing.T) {
			copy := map[string]any{}
			for key, value := range result {
				if key != field {
					copy[key] = value
				}
			}
			if decodeExistingResponse(encode(copy), envelope, p, &decoded) == nil {
				t.Fatal("missing field accepted")
			}
		})
	}
	for _, field := range []string{"model_id", "state", "process_active", "api_ready", "endpoint", "engine_log", "action_log", "message", "generation"} {
		t.Run("null_"+field, func(t *testing.T) {
			copy := map[string]any{}
			for key, value := range result {
				copy[key] = value
			}
			copy[field] = nil
			if decodeExistingResponse(encode(copy), envelope, p, &decoded) == nil {
				t.Fatal("null field accepted")
			}
		})
	}
	for _, field := range []string{"unknown", "reason", "effect_invoked"} {
		copy := map[string]any{}
		for key, value := range result {
			copy[key] = value
		}
		copy[field] = false
		if decodeExistingResponse(encode(copy), envelope, p, &decoded) == nil {
			t.Fatal("mixed/unknown field accepted", field)
		}
	}
	duplicate := strings.Replace(string(encode(result)), `"state":"READY"`, `"state":"READY","state":"READY"`, 1)
	if decodeExistingResponse([]byte(duplicate), envelope, p, &decoded) == nil {
		t.Fatal("duplicate field accepted")
	}
	noop := existingRequest{OperationID: "no-effect", ProfileID: p.ID, Action: "start", ExpectedGeneration: 7}
	noopEnvelope := existingEnvelope{SchemaVersion: 1, Method: "receipt", Request: &noop}
	refusal := []byte(`{"schema_version":1,"receipt":{"operation_id":"no-effect","profile_id":"` + p.ID + `","action":"start","phase":"completed","run_id":null,"generation":7,"attempt":null,"result":{"reason":"dispatch_expired","effect_invoked":false}}}`)
	if err := decodeExistingResponse(refusal, noopEnvelope, p, &decoded); err != nil {
		t.Fatal("explicit no-effect schema rejected", err)
	}
	for _, bad := range []string{strings.Replace(string(refusal), `"effect_invoked":false`, `"effect_invoked":null`, 1), strings.Replace(string(refusal), `"effect_invoked":false`, `"effect_invoked":true`, 1), strings.Replace(string(refusal), `"dispatch_expired"`, `"arbitrary_reason"`, 1), strings.Replace(string(refusal), `"reason":"dispatch_expired",`, "", 1)} {
		if decodeExistingResponse([]byte(bad), noopEnvelope, p, &decoded) == nil {
			t.Fatal("invalid refusal schema accepted")
		}
	}
}

func TestExistingActualPythonControllerOutputDecodes(t *testing.T) {
	raw, err := os.ReadFile("testdata/existing-coordinator-python-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		SchemaVersion int            `json:"schema_version"`
		Provenance    map[string]any `json:"provenance"`
		Cases         []struct {
			Name     string          `json:"name"`
			Request  json.RawMessage `json:"request"`
			Response json.RawMessage `json:"response"`
		} `json:"cases"`
	}
	if err = strictDecode(raw, &fixture); err != nil || fixture.SchemaVersion != 1 || len(fixture.Cases) != 12 {
		t.Fatalf("fixture: %v", err)
	}
	_, _, profile := existingFixture(t)
	for _, item := range fixture.Cases {
		t.Run(item.Name, func(t *testing.T) {
			var envelope existingEnvelope
			if err := strictDecode(item.Request, &envelope); err != nil {
				t.Fatal(err)
			}
			response := item.Response
			if envelope.Method == "observe_many" {
				var archived existingResponse
				if decodeExistingResponse(response, envelope, profile, &archived) == nil {
					t.Fatal("historical snapshot accepted as current")
				}
				// Only freshness time is rebased; every actual producer schema/identity/proof
				// field remains frozen. This does not dispatch the archived request.
				var data map[string]any
				if err := json.Unmarshal(response, &data); err != nil {
					t.Fatal(err)
				}
				for _, model := range data["models"].([]any) {
					model.(map[string]any)["observed_at_ms"] = time.Now().UnixMilli()
				}
				response, err = json.Marshal(data)
				if err != nil {
					t.Fatal(err)
				}
			}
			var decoded existingResponse
			if err := decodeExistingResponse(response, envelope, profile, &decoded); err != nil {
				t.Fatalf("actual Python output rejected: %v\n%s", err, response)
			}
			if decoded.Receipt != nil && decoded.Receipt.Result != nil && decoded.Receipt.Result.ShutdownVerified && !validExistingShutdown(*decoded.Receipt.Result, profile.ID, decoded.Receipt.RunID, decoded.Receipt.Generation) {
				t.Fatal("actual physical stop witness rejected")
			}
		})
	}
}
