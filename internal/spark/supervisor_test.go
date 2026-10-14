package spark

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func rootDefinitionFixture(t *testing.T) (Profile, map[string]string) {
	t.Helper()
	fragment := "/usr/lib/systemd/system/basic.target"
	if _, e := os.Stat(fragment); e != nil {
		t.Skip("native protected systemd definition fixture absent")
	}
	exe, e := filepath.EvalSymlinks("/usr/bin/true")
	if e != nil {
		t.Skip("native protected true executable absent")
	}
	p := Profile{Target: Target{Kind: "systemd-system", Unit: "fixture.service", AllowRootUnit: true, TrustedFiles: []string{exe}}}
	m := map[string]string{"FragmentPath": fragment, "User": "root", "ExecStart": "{ path=" + exe + " ; argv[]=" + exe + " ; ignore_errors=no ; start_time=[n/a] ; stop_time=[n/a] ; pid=0 ; code=(null) ; status=0/0 }"}
	return p, m
}
func TestEffectiveSystemdDefinitionExcludesRuntimeButPinsCode(t *testing.T) {
	p, m := rootDefinitionFixture(t)
	before, e := canonicalSystemdDefinition(p, m)
	if e != nil {
		t.Fatal(e)
	}
	p, m = rootDefinitionFixture(t)
	m["ExecStart"] = "{ path=/usr/bin/true ; argv[]=/usr/bin/true ; ignore_errors=no ; start_time=[Wed 2026-10-14 10:00:00 MSK] ; stop_time=[Wed 2026-10-14 10:00:01 MSK] ; pid=11235 ; code=exited ; status=0 }"
	m["ExecStart"] = strings.ReplaceAll(m["ExecStart"], "/usr/bin/true", p.Target.TrustedFiles[0])
	after, e := canonicalSystemdDefinition(p, m)
	if e != nil {
		t.Fatal(e)
	}
	if before != after {
		t.Fatal("normal lifecycle runtime fields changed effective approval")
	}
	p, m = rootDefinitionFixture(t)
	m["ExecStart"] = "{ path=/usr/bin/true ; argv[]=/usr/bin/true --changed ; ignore_errors=no ; start_time=[n/a] ; pid=0 }"
	m["ExecStart"] = strings.ReplaceAll(m["ExecStart"], "/usr/bin/true", p.Target.TrustedFiles[0])
	changed, e := canonicalSystemdDefinition(p, m)
	if e != nil {
		t.Fatal(e)
	}
	if before == changed {
		t.Fatal("effect argument drift ignored")
	}
}
func TestRootShellOwnerWritableScriptAndUnrelatedEnvPinRejected(t *testing.T) {
	p, m := rootDefinitionFixture(t)
	p.Target.TrustedFiles = []string{"/usr/bin/bash"}
	script := filepath.Join(t.TempDir(), "mutable-launch.sh")
	if e := os.WriteFile(script, []byte("#!/bin/sh\ntrue\n"), 0755); e != nil {
		t.Fatal(e)
	}
	m["ExecStart"] = "{ path=/usr/bin/bash ; argv[]=/usr/bin/bash " + script + " ; ignore_errors=no ; start_time=[n/a] }"
	if _, e := canonicalSystemdDefinition(p, m); e == nil {
		t.Fatal("root interpreter accepted owner-writable unpinned script")
	}
	p, m = rootDefinitionFixture(t)
	m["EnvironmentFiles"] = "/etc/owner-env (ignore_errors=no)"
	if _, e := canonicalSystemdDefinition(p, m); e == nil {
		t.Fatal("unrelated trusted file approved environment file")
	}
	p, m = rootDefinitionFixture(t)
	p.Target.TrustedFiles = []string{"/usr/bin/bash"}
	m["ExecStart"] = "{ path=/usr/bin/bash ; argv[]=/usr/bin/bash -c echo dangerous ; ignore_errors=no ; pid=0 }"
	if _, e := canonicalSystemdDefinition(p, m); e == nil {
		t.Fatal("root shell payload approved")
	}
}
