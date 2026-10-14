package spark

import (
	"strings"
	"testing"
	"time"
)

func TestExistingLegacyPhysicalDeclarationAndExactSupervisor(t *testing.T) {
	for _, id := range []string{"qwen-existing-observation", "gpt-oss-120b-existing-configured"} {
		t.Run(id, func(t *testing.T) {
			_, _, p := existingFixture(t)
			p.ID = id
			run, attempt, cid, created, birth, declaration := strings.Repeat("a", 32), strings.Repeat("b", 32), strings.Repeat("c", 64), "immutable-created", "123456", strings.Repeat("d", 64)
			generation, pid := int64(9), 42
			unit := "spark-v4-" + id + "@" + run + ".service"
			if id == "qwen-existing-observation" {
				unit = "spark-sglang.service"
			}
			i := &existingPhysicalInstance{RunID: &run, Owner: &existingOwner{Unit: unit, InvocationID: strings.Repeat("e", 32), PID: 41, Birth: "123455"}, ContainerID: &cid, ContainerCreated: &created, EnginePID: &pid, EngineBirth: &birth, LegacyDeclarationSHA256: &declaration}
			s := existingSnapshot{existingResult: existingResult{ModelID: id, State: "READY", ProcessActive: true, APIReady: true, Generation: &generation, Attempt: &attempt, RunID: &run, InstanceIdentity: existingPhysicalIdentity(i)}, ProfileRevision: p.Target.Existing.BackendRevision, ObservedAtMS: time.Now().UnixMilli(), Instance: i}
			if err := validateExistingSnapshot(s, p); err != nil {
				t.Fatalf("qualified legacy physical observation rejected: %v", err)
			}
			changed := s
			changedInstance := *i
			changedDeclaration := strings.Repeat("f", 64)
			changedInstance.LegacyDeclarationSHA256 = &changedDeclaration
			changed.Instance, changed.InstanceIdentity = &changedInstance, existingPhysicalIdentity(&changedInstance)
			if err := validateExistingSnapshot(changed, p); err != nil {
				t.Fatalf("independently shaped changed observation rejected: %v", err)
			}
			if compatibleExistingPhysical(&s, &changed) {
				t.Fatal("pending original observation borrowed a new captured declaration")
			}
			for _, cause := range []string{"missing_declaration", "changed_declaration", "invalid_declaration", "foreign_unit"} {
				bad := s
				physical, owner := *i, *i.Owner
				bad.Instance, physical.Owner = &physical, &owner
				switch cause {
				case "missing_declaration":
					physical.LegacyDeclarationSHA256 = nil
				case "changed_declaration":
					changed := strings.Repeat("f", 64)
					physical.LegacyDeclarationSHA256 = &changed
				case "invalid_declaration":
					invalid := "unknown"
					physical.LegacyDeclarationSHA256 = &invalid
				case "foreign_unit":
					owner.Unit = "foreign.service"
					bad.InstanceIdentity = existingPhysicalIdentity(&physical)
				}
				if validateExistingSnapshot(bad, p) == nil {
					t.Fatal("accepted " + cause)
				}
			}
			w := &existingShutdownWitness{Kind: "exact_run_resources_released", ModelID: id, RunID: &run, Generation: generation, Attempt: &attempt, InstanceIdentity: s.InstanceIdentity, Owner: i.Owner, ContainerID: i.ContainerID, ContainerCreated: i.ContainerCreated, EnginePID: i.EnginePID, EngineBirth: i.EngineBirth, LegacyDeclarationSHA256: &declaration}
			stopped := s.existingResult
			stopped.State, stopped.ProcessActive, stopped.APIReady, stopped.ShutdownVerified, stopped.ShutdownWitness = "STOPPED", false, false, true, w
			if !validExistingShutdown(stopped, id, &run, generation) {
				t.Fatal("qualified legacy stopped instance rejected")
			}
			w.LegacyDeclarationSHA256 = nil
			if validExistingShutdown(stopped, id, &run, generation) {
				t.Fatal("shutdown proof accepted an omitted declaration bound into its identity")
			}
			// Before container creation the qualified declaration does not yet
			// exist. This partial physical state cannot establish READY.
			partial := s
			partial.State, partial.ProcessActive, partial.APIReady = "STARTING", false, false
			partial.Instance = &existingPhysicalInstance{RunID: &run}
			partial.InstanceIdentity = existingPhysicalIdentity(partial.Instance)
			if err := validateExistingSnapshot(partial, p); err != nil {
				t.Fatalf("truthful pre-create partial state rejected: %v", err)
			}
			if !compatibleExistingPhysical(&partial, &s) {
				t.Fatal("first exact declaration capture rejected for the same original run")
			}
			partial.State, partial.ProcessActive, partial.APIReady = "READY", true, true
			if validateExistingSnapshot(partial, p) == nil {
				t.Fatal("partial creation established READY")
			}
			p.ID = "ling-3-flash-124b-heretic-text"
			s.ModelID = p.ID
			if validateExistingSnapshot(s, p) == nil {
				t.Fatal("ordinary V4 accepted an unapproved legacy declaration field")
			}
		})
	}
}
