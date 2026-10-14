package spark

import (
	"os"
	"path/filepath"
	"testing"
)

const shutdownTestBoot = "01234567-89ab-cdef-0123-456789abcdef"

func shutdownSystemdProperties() map[string]string {
	return map[string]string{"LoadState": "loaded", "ActiveState": "failed", "SubState": "failed", "InvocationID": "57c1ea4f35a7489c9ba4520341a1fc5a", "MainPID": "0", "ControlPID": "0", "Job": "", "ControlGroup": "", "ExecMainStartTimestampMonotonic": "0"}
}

func TestSystemdFailedShutdownRequiresCompleteCurrentProof(t *testing.T) {
	p := Profile{Target: Target{Kind: "systemd-system", Unit: "canary.service"}}
	for _, active := range []string{"failed", "inactive"} {
		t.Run(active, func(t *testing.T) {
			m := shutdownSystemdProperties()
			m["ActiveState"] = active
			if active == "inactive" {
				m["SubState"] = "dead"
			}
			got, err := systemdInstance(p, m, shutdownTestBoot, nil)
			wantState := "failed"
			if active == "inactive" {
				wantState = "stopped"
			}
			if err != nil || got.State != wantState || !got.ShutdownVerified || !shutdownComplete(p, got) || got.Identity == "" || active == "failed" && got.Identity == "stopped" {
				t.Fatalf("complete physical shutdown=%+v err=%v", got, err)
			}
			for key := range m {
				t.Run("missing-"+key, func(t *testing.T) {
					value := m[key]
					delete(m, key)
					defer func() { m[key] = value }()
					if i, err := systemdInstance(p, m, shutdownTestBoot, nil); err == nil || i.ShutdownVerified {
						t.Fatalf("missing %s accepted: %+v %v", key, i, err)
					}
				})
			}
			for key, values := range map[string][]string{"MainPID": {"", "-1", "00", " 0", "1", "4294967296"}, "ControlPID": {"", "unknown", "00", "1"}, "Job": {"0", "10", "10 /org/freedesktop/systemd1/job/10", " "}, "SubState": {"", "start-pre"}, "ControlGroup": {"/system.slice/canary.service"}} {
				for _, value := range values {
					t.Run(key+"="+value, func(t *testing.T) {
						old := m[key]
						m[key] = value
						defer func() { m[key] = old }()
						if i, _ := systemdInstance(p, m, shutdownTestBoot, func(string) bool { return false }); i.ShutdownVerified || shutdownComplete(p, i) {
							t.Fatalf("ambiguous/live %s=%q proved shutdown: %+v", key, value, i)
						}
					})
				}
			}
		})
	}
	m := shutdownSystemdProperties()
	first, _ := systemdInstance(p, m, shutdownTestBoot, nil)
	m["InvocationID"] = "67c1ea4f35a7489c9ba4520341a1fc5a"
	other, _ := systemdInstance(p, m, shutdownTestBoot, nil)
	if first.Identity == other.Identity {
		t.Fatal("failed service invocation identity collapsed")
	}
	if i, err := systemdInstance(p, m, "", nil); err == nil || i.ShutdownVerified {
		t.Fatal("missing boot identity accepted")
	}
	for _, invocation := range []string{"", "stopped", "57c1ea4f35a7489c9ba4520341a1fc5", "57c1ea4f35a7489c9ba4520341a1fc5Z"} {
		m["InvocationID"] = invocation
		if i, err := systemdInstance(p, m, shutdownTestBoot, nil); err == nil || i.ShutdownVerified {
			t.Fatalf("ambiguous failed invocation %q accepted", invocation)
		}
	}
	m["ActiveState"], m["SubState"], m["InvocationID"] = "inactive", "dead", ""
	if i, err := systemdInstance(p, m, shutdownTestBoot, nil); err != nil || !i.ShutdownVerified || i.Identity != "stopped" {
		t.Fatalf("never-started inactive service=%+v err=%v", i, err)
	}
}

func TestSystemdRetainedCgroupMustBeObservedEmptyIncludingChildren(t *testing.T) {
	root := t.TempDir()
	group := "/system.slice/canary.service"
	path := filepath.Join(root, group)
	if err := os.MkdirAll(filepath.Join(path, "child"), 0700); err != nil {
		t.Fatal(err)
	}
	write := func(name, value string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(path, name), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("cgroup.events", "populated 0\nfrozen 0\n")
	write("cgroup.procs", "")
	write("child/cgroup.procs", "")
	p := Profile{Target: Target{Kind: "systemd-system", Unit: "canary.service"}}
	m := shutdownSystemdProperties()
	m["ControlGroup"] = group
	inspect := func() Instance {
		i, err := systemdInstance(p, m, shutdownTestBoot, func(group string) bool { return systemdCgroupEmpty(root, group) })
		if err != nil {
			t.Fatal(err)
		}
		return i
	}
	if !inspect().ShutdownVerified {
		t.Fatal("explicit empty parent/child cgroup observation rejected")
	}
	for _, sample := range []struct{ name, value string }{{"child/cgroup.procs", "4411\n"}, {"cgroup.procs", "4422\n"}, {"cgroup.events", "populated 1\nfrozen 0\n"}, {"cgroup.events", "frozen 0\n"}, {"cgroup.events", "populated 0\npopulated 0\n"}, {"cgroup.events", "populated unknown\n"}} {
		t.Run(sample.name+"="+sample.value, func(t *testing.T) {
			write(sample.name, sample.value)
			if inspect().ShutdownVerified {
				t.Fatal("live/ambiguous cgroup accepted")
			}
			write(sample.name, "")
			write("cgroup.events", "populated 0\nfrozen 0\n")
		})
	}
	if err := os.Remove(filepath.Join(path, "child/cgroup.procs")); err != nil {
		t.Fatal(err)
	}
	if inspect().ShutdownVerified {
		t.Fatal("missing descendant process observation accepted")
	}
	write("child/cgroup.procs", "")
	if err := os.Symlink(t.TempDir(), filepath.Join(path, "outside")); err != nil {
		t.Fatal(err)
	}
	if inspect().ShutdownVerified {
		t.Fatal("symlink escaped cgroup boundary")
	}
	for _, group := range []string{"/", "relative", "/system.slice/../outside", "/missing.service"} {
		if systemdCgroupEmpty(root, group) {
			t.Fatalf("unverified cgroup %q accepted", group)
		}
	}
}
