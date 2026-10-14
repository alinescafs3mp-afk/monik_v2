package spark

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
)

type failedShutdownSupervisor struct{ *fakeSupervisor }

func (s failedShutdownSupervisor) Effect(ctx context.Context, p Profile, action string) (Instance, error) {
	i, err := s.fakeSupervisor.Effect(ctx, p, action)
	if action == "stop" {
		// Explicit complete adapter observation, not inferred from a fake's
		// failed state or synthetic identity="stopped".
		i, _ = systemdInstance(p, shutdownSystemdProperties(), shutdownTestBoot, nil)
		s.mu.Lock()
		s.instances[p.ID] = i
		s.mu.Unlock()
	}
	return i, err
}

func TestFailedPhysicalShutdownClosesExactStartWithoutChangingFailureState(t *testing.T) {
	for _, lostReply := range []bool{false, true} {
		t.Run(map[bool]string{false: "observed", true: "lost-stop-reply"}[lostReply], func(t *testing.T) {
			c, f, p := controlFixture(t)
			c.supervisor = failedShutdownSupervisor{f}
			f.starting = true
			start := actionRequest(c, p, "start-before-failed-shutdown", "start")
			if got := c.Execute(context.Background(), start); got.Status != protocol.TargetRunning {
				t.Fatalf("start=%+v", got)
			}
			f.failAfter = lostReply
			stop := actionRequest(c, p, "failed-shutdown-stop", "stop")
			got := c.Execute(context.Background(), stop)
			if lostReply {
				if got.Status != protocol.TargetUnknownResult {
					t.Fatalf("lost stop reply fabricated success: %+v", got)
				}
				got = c.Execute(context.Background(), stop)
			}
			if got.Status != protocol.TargetSucceeded || got.Stage != "model_stopped" || got.Evidence["supervisor_state"] != "failed" || got.Evidence["shutdown_verified"] != true || got.Evidence["reason"] != "failed_service_shutdown_verified" || got.Evidence["readiness"] != "not_applicable" || got.Evidence["instance_identity"] == "stopped" {
				t.Fatalf("physical stop proof=%+v", got)
			}
			for _, reopen := range []bool{false, true} {
				if reopen {
					var err error
					c, err = OpenCoordinator(c.Policy, failedShutdownSupervisor{f})
					if err != nil {
						t.Fatal(err)
					}
					if err = c.Bind(c.Policy.AgentID, c.Policy.ControllerID, "session-a"); err != nil {
						t.Fatal(err)
					}
				}
				cancelled := c.Execute(context.Background(), start)
				if cancelled.Status != protocol.TargetFailed || cancelled.Stage != "start_cancelled_after_stop" || cancelled.Evidence["supervisor_state"] != "failed" || cancelled.Evidence["shutdown_verified"] != true || !strings.Contains(cancelled.Message, stop.OperationID) {
					t.Fatalf("exact start cancellation reopen=%v: %+v", reopen, cancelled)
				}
				snapshot, err := c.Snapshot(context.Background())
				if err != nil || snapshot.States[0].SupervisorState != "failed" || snapshot.States[0].Readiness != "not_applicable" || snapshot.States[0].Reason != "failed_service_shutdown_verified" || snapshot.States[0].Quality != "ok" || snapshot.States[0].Generation != c.journal.Profiles[p.ID].Generation {
					t.Fatalf("failure state erased or shutdown proof missing: %+v %v", snapshot, err)
				}
			}
			f.failAfter, f.starting = false, false
			if got := c.Execute(context.Background(), actionRequest(c, p, "fresh-after-failed-stop", "start")); got.Status != protocol.TargetSucceeded {
				t.Fatalf("fresh start after verified closure=%+v", got)
			}
			if f.effects[p.ID+":start"] != 2 || f.effects[p.ID+":stop"] != 1 {
				t.Fatalf("duplicate stop/start effects=%v", f.effects)
			}
		})
	}
}

func TestFailedShutdownAllowsFreshLinkedRestartStageAndLocalCycle(t *testing.T) {
	for _, local := range []bool{false, true} {
		t.Run(map[bool]string{false: "remote-stage", true: "local-cycle"}[local], func(t *testing.T) {
			c, f, p := controlFixture(t)
			c.supervisor = failedShutdownSupervisor{f}
			if got := c.Execute(context.Background(), actionRequest(c, p, "initial-running", "start")); got.Status != protocol.TargetSucceeded {
				t.Fatalf("initial=%+v", got)
			}
			var got protocol.JobReceipt
			if local {
				got = c.ExecuteLocal(context.Background(), "local-restart", p.ID, "restart", true)
			} else {
				restart := actionRequest(c, p, "remote-restart", "restart")
				stopped := c.Execute(context.Background(), restart)
				if stopped.Status != protocol.TargetAwaitingConfirmation || stopped.Stage != "restart_stopped" || stopped.Evidence["supervisor_state"] != "failed" || stopped.Evidence["shutdown_verified"] != true {
					t.Fatalf("restart physical stop=%+v", stopped)
				}
				stage := actionRequest(c, p, "fresh-linked-start", "start")
				stage.ParentJobID, stage.OperationID = restart.JobID, restart.OperationID
				got = c.Execute(context.Background(), stage)
			}
			wantStage := "model_started"
			if local {
				wantStage = "model_restarted"
			}
			if got.Status != protocol.TargetSucceeded || got.Stage != wantStage || f.effects[p.ID+":start"] != 2 || f.effects[p.ID+":stop"] != 1 {
				t.Fatalf("linked restart=%+v effects=%v", got, f.effects)
			}
		})
	}
}

func TestTerminalStatesWithoutCurrentShutdownProofStayUnresolved(t *testing.T) {
	for _, sample := range []struct {
		name string
		i    Instance
	}{
		{"arbitrary-failed", Instance{State: "failed", Identity: "failed-invocation", Available: true}},
		{"failed-stopped-identity", Instance{State: "failed", Identity: "stopped", Available: true, ShutdownVerified: true}},
		{"inactive-queued-job", Instance{State: "stopped", Identity: "inactive-with-job", Available: true}},
		{"missing-current-availability", Instance{State: "failed", Identity: "failed-invocation", ShutdownVerified: true}},
		{"remaining-pid", Instance{State: "stopped", Identity: "stopped", PID: 44, Available: true, ShutdownVerified: true}},
	} {
		t.Run(sample.name, func(t *testing.T) {
			c, f, p := controlFixture(t)
			stop := actionRequest(c, p, "uncertain-stop", "stop")
			a := attempt{Request: stop, Stage: "effect_attempted", At: time.Now()}
			if got := c.reconcileLocked(&a, p, sample.i); got.Status != protocol.TargetUnknownResult || a.Stage != "effect_attempted" || got.Evidence["shutdown_verified"] == true {
				t.Fatalf("ambiguous shutdown fabricated closure: %+v attempt=%+v", got, a)
			}
			f.instances[p.ID] = sample.i
			snapshot, err := c.Snapshot(context.Background())
			if err != nil || snapshot.States[0].Readiness == "not_applicable" || snapshot.States[0].Reason == "failed_service_shutdown_verified" {
				t.Fatalf("ambiguous current state fabricated snapshot closure: %+v %v", snapshot, err)
			}
			if got := c.Execute(context.Background(), actionRequest(c, p, "unsafe-new-start", "start")); got.ErrorCode != "service_shutdown_unverified" || len(f.effects) != 0 {
				t.Fatalf("unsafe stopped/failed state admitted start: %+v effects=%v", got, f.effects)
			}
		})
	}
}

func TestFailedStopReceiptCannotCancelStartWithoutTypedPhysicalProof(t *testing.T) {
	for _, missing := range []string{"shutdown_verified", "readiness", "reason", "instance_identity"} {
		t.Run(missing, func(t *testing.T) {
			c, _, p := controlFixture(t)
			start := actionRequest(c, p, "bound-start", "start")
			stop := actionRequest(c, p, "unproven-stop", "stop")
			c.journal.Attempts[start.JobID] = attempt{Request: start, Stage: "effect_attempted", StoppedByJobID: stop.JobID}
			i, _ := systemdInstance(p, shutdownSystemdProperties(), shutdownTestBoot, nil)
			receipt := c.evidenceReceipt(stop, p, i, protocol.TargetSucceeded, "model_stopped")
			delete(receipt.Evidence, missing)
			c.finishStoppedStartsLocked(attempt{Request: stop, Stage: "done", Receipt: receipt})
			if c.journal.Attempts[start.JobID].Stage == "done" {
				t.Fatal("incomplete failed stop receipt closed start")
			}
		})
	}
}
