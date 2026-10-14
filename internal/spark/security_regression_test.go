package spark

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestSecurityReviewRootRelativeScriptRejected(t *testing.T) {
	p, m := rootDefinitionFixture(t)
	p.Target.TrustedFiles = []string{"/usr/bin/bash"}
	m["WorkingDirectory"] = t.TempDir()
	m["ExecStart"] = "{ path=/usr/bin/bash ; argv[]=/usr/bin/bash mutable-launch.sh ; ignore_errors=no ; pid=0 }"
	if _, e := canonicalSystemdDefinition(p, m); e == nil {
		t.Fatal("accepted root bash script in owner-writable WorkingDirectory without pinning it")
	}
}
func TestSecurityReviewRootVersionedInterpreterRejected(t *testing.T) {
	p, m := rootDefinitionFixture(t)
	exe, e := filepath.EvalSymlinks("/usr/bin/python3")
	if e != nil {
		t.Skip(e)
	}
	if _, e = os.Stat(exe); e != nil {
		t.Skip(e)
	}
	p.Target.TrustedFiles = []string{exe}
	m["ExecStart"] = "{ path=" + exe + " ; argv[]=" + exe + " -c print(123) ; ignore_errors=no ; pid=0 }"
	if _, e = canonicalSystemdDefinition(p, m); e == nil {
		t.Fatalf("accepted inline root payload through %s", exe)
	}
}
func TestSecurityReviewRootMutablePreloadRejected(t *testing.T) {
	p, m := rootDefinitionFixture(t)
	m["Environment"] = "LD_PRELOAD=/home/unprivileged/mutable.so"
	if _, e := canonicalSystemdDefinition(p, m); e == nil {
		t.Fatal("accepted mutable LD_PRELOAD on root unit")
	}
}
func TestSecurityReviewFrozenDockerSecurityDriftRejected(t *testing.T) {
	d := DockerDeclaration{Name: "fixture", Image: "repo@sha256:" + strings.Repeat("a", 64), User: "1000", Command: []string{"model-server"}, Environment: []string{}, NetworkMode: "none", Mounts: []DockerMount{}, StopTimeout: 60}
	config, _ := json.Marshal(map[string]any{"Image": d.Image, "User": d.User, "Cmd": d.Command, "Env": d.Environment, "WorkingDir": d.WorkingDir, "Entrypoint": []string{}})
	hc, _ := json.Marshal(map[string]any{"NetworkMode": d.NetworkMode, "Mounts": []any{}, "CapDrop": []string{}, "SecurityOpt": []string{}})
	if e := dockerMatchesFrozen(dockerObject{Config: config, HostConfig: hc}, d); e == nil {
		t.Fatal("accepted recycled frozen container without ALL capability drop or no-new-privileges")
	}
}
func TestSecurityReviewANSISecretRemainsRedacted(t *testing.T) {
	r := &Redactor{secrets: []string{"sensitive-fixture-secret"}}
	got := r.Clean([]byte("sensitive-fixture-\x1b[31msecret"))
	if strings.Contains(got, "sensitive-fixture-secret") {
		t.Fatalf("normalization exposed configured secret: %q", got)
	}
	got = (&Redactor{}).Clean([]byte("hf_ABCDEF\x1b[31mGHIJKLMNOPQRST"))
	if strings.Contains(got, "hf_ABCDEFGHIJKLMNOPQRST") {
		t.Fatalf("normalization exposed HF token: %q", got)
	}
}

// The fake effect is an ambiguous supervisor failure whose actual state remains
// unchanged. It never launches a real process.
type securityStationarySupervisor struct{ *fakeSupervisor }

func (s securityStationarySupervisor) Effect(_ context.Context, p Profile, action string) (Instance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.effects[p.ID+":"+action]++
	return Instance{State: "stopped", Identity: "stopped", Available: true}, fmt.Errorf("synthetic ambiguous effect")
}
func TestSecurityReviewConcurrentDuplicateCannotRepeatAmbiguousEffect(t *testing.T) {
	for iteration := 0; iteration < 200; iteration++ {
		c, f, p := controlFixture(t)
		c.supervisor = securityStationarySupervisor{f}
		weight := filepath.Join(c.Policy.StateDir, "fixture.gguf")
		if e := os.WriteFile(weight, []byte("synthetic"), 0600); e != nil {
			t.Fatal(e)
		}
		p.Weights = []string{weight}
		p.Revision = ProfileRevision(p)
		c.Policy.Profiles[0] = p
		r := actionRequest(c, p, "same-ambiguous-request", "start")
		var wg sync.WaitGroup
		launch := make(chan struct{})
		for n := 0; n < 16; n++ {
			wg.Add(1)
			go func() { defer wg.Done(); <-launch; c.Execute(context.Background(), r) }()
		}
		close(launch)
		wg.Wait()
		if got := f.effects[p.ID+":start"]; got > 1 {
			t.Fatalf("iteration %d repeated same ambiguous effect %d times", iteration, got)
		}
	}
}
