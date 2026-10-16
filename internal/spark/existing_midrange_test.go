package spark

import (
	"context"
	"strings"
	"testing"
)

func midrangeProfile(t *testing.T, first Profile, id string) Profile {
	t.Helper()
	p := first
	p.ID = id
	a := *first.Target.Existing
	a.CodePins = nil
	p.Target.Existing = &a
	for _, path := range existingRequiredPaths(p) {
		a.CodePins = append(a.CodePins, ExistingCodePin{path, strings.Repeat("b", 64)})
	}
	p.Target.DefinitionHash = digest(&a)
	p.Revision = ProfileRevision(p)
	return p
}

func TestExistingMidrangeFixedIDsNeedSameCompleteNativeSourcePins(t *testing.T) {
	_, _, first := existingFixture(t)
	for _, id := range existingIDs[7:] {
		p := midrangeProfile(t, first, id)
		if err := p.validateExistingCoordinator(); err != nil {
			t.Fatal(id, err)
		}
		if existingLegacyID(id) {
			t.Fatal("new native ID treated as legacy")
		}
		p.Target.Existing.CodePins[6].Path = "/etc/systemd/system/spark-v4-other@.service"
		p.Target.DefinitionHash = digest(p.Target.Existing)
		if p.validateExistingCoordinator() == nil {
			t.Fatal("foreign exact unit accepted")
		}
	}
	p := midrangeProfile(t, first, "arbitrary-user-model")
	if p.validateExistingCoordinator() == nil {
		t.Fatal("unknown fixed ID accepted")
	}
}

func TestExistingTenCatalogObservesOneFreshStrictRound(t *testing.T) {
	c, f, profiles := existingRoundFixture(t)
	for _, id := range existingIDs[7:] {
		profiles = append(profiles, midrangeProfile(t, profiles[0], id))
	}
	c.Policy.Profiles = profiles
	if err := c.Policy.Validate(); err != nil {
		t.Fatal(err)
	}
	report, err := c.Snapshot(context.Background())
	if err != nil || len(report.States) != 10 || !report.Inventory.Complete || f.batchCalls != 1 {
		t.Fatalf("new catalog round %+v %v", report, err)
	}
	for i, p := range profiles {
		if report.States[i].ProfileID != p.ID {
			t.Fatal("foreign or reordered profile")
		}
	}
	f.mutate = func(s []existingSnapshot) { s[9].ModelID = profiles[0].ID }
	report, err = c.Snapshot(context.Background())
	if err == nil && report.Inventory.Complete {
		t.Fatal("foreign new-model response accepted")
	}
	if f.batchCalls != 2 {
		t.Fatal("unexpected retry or old round reuse")
	}
}
