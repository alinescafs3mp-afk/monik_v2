package spark

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"golang.org/x/sys/unix"
)

func TestSecurityClosureRootLoaderAndEffectiveDefinitionBoundaries(t *testing.T) {
	for _, wrapper := range []string{"timeout", "nice", "nohup", "setsid", "stdbuf", "flock", "taskset", "ionice", "chrt", "prlimit", "setpriv", "numactl", "xargs", "systemd-run", "unshare", "chroot", "docker"} {
		if interpreterKind(wrapper) != "unsupported" {
			t.Fatalf("root executable-resolving wrapper accepted: %s", wrapper)
		}
		exe, err := filepath.EvalSymlinks("/usr/bin/" + wrapper)
		if err != nil {
			continue
		}
		p, m := rootDefinitionFixture(t)
		p.Target.TrustedFiles = []string{exe}
		m["ExecStart"] = "{ path=" + exe + " ; argv[]=" + exe + " /home/unprivileged/mutable-launch.sh ; ignore_errors=no }"
		if _, err = canonicalSystemdDefinition(p, m); err == nil {
			t.Fatalf("pinned root native wrapper approved mutable nested code: %s", wrapper)
		}
	}
	for _, utility := range []string{"tar", "ssh", "gdb", "make", "git", "rsync", "sed", "find", "cp", "curl"} {
		exe, err := filepath.EvalSymlinks("/usr/bin/" + utility)
		if err != nil {
			continue
		}
		p, m := rootDefinitionFixture(t)
		p.Target.TrustedFiles = []string{exe}
		m["ExecStart"] = "{ path=" + exe + " ; argv[]=" + exe + " --arbitrary-code-hook=/home/owner/code ; ignore_errors=no }"
		if _, err = canonicalSystemdDefinition(p, m); err == nil {
			t.Fatalf("generic root utility admitted outside positive argv contract: %s", utility)
		}
	}
	sleep, err := filepath.EvalSymlinks("/usr/bin/sleep")
	if err == nil && (!supportedRootCommand(sleep, []string{sleep, "2.5s"}) || supportedRootCommand(sleep, []string{sleep, "--command=/home/owner/code"})) {
		t.Fatal("typed native helper argv admission incorrect")
	}
	for _, name := range []string{"perl5.40.0", "node22", "nodejs24", "node-v22", "ruby3.4", "lua5.4", "php8.4", "tclsh8.6", "python-unknown-loader", "bash5.2", "env", "ld-linux-aarch64.so.1", "ld-musl-x86_64.so.1", "ld64.so.2"} {
		if interpreterKind(name) != "unsupported" {
			t.Fatalf("unclassified root loader %s", name)
		}
	}
	for _, name := range []string{"python3.13t", "python3.14", "python2.7mu", "pypy3.11"} {
		if interpreterKind(name) != "python" {
			t.Fatalf("unclassified versioned Python loader %s", name)
		}
	}
	for _, env := range []string{"NODE_OPTIONS=--require=/home/owner/code.js", "PERL5OPT=-Mmutable", "PERL5LIB=/home/owner/modules", "LD_PRELOAD = /home/owner/payload.so", "GCONV_PATH=/home/owner/code", "PATH=/home/owner/bin", "HOME=/home/owner", "PYTHONUSERBASE=/home/owner"} {
		p, m := rootDefinitionFixture(t)
		m["Environment"] = env
		if _, err := canonicalSystemdDefinition(p, m); err == nil {
			t.Fatalf("accepted root loader environment %q", env)
		}
	}
	p, m := rootDefinitionFixture(t)
	m["Environment"] = "LANG=C CUDA_VISIBLE_DEVICES=0 PATH=/usr/bin"
	m["ExecCondition"] = m["ExecStart"]
	if _, err := canonicalSystemdDefinition(p, m); err != nil {
		t.Fatalf("safe pinned native root commands rejected: %v", err)
	}
	for _, field := range []string{"MountImages", "JoinsNamespaceOf", "TemporaryFileSystem", "ExtensionDirectories"} {
		p, m := rootDefinitionFixture(t)
		m[field] = "/home/owner/mutable"
		if _, err := canonicalSystemdDefinition(p, m); err == nil {
			t.Fatalf("accepted root execution namespace %s", field)
		}
	}
	for _, mutation := range []string{"flags=privileged", "flags=unknown-future", "argv[]=/usr/bin/another-command", "path=/usr/bin/true ; path=/usr/bin/true", "missing=value", "flags=no-setuid"} {
		p, m := rootDefinitionFixture(t)
		exe := p.Target.TrustedFiles[0]
		m["ExecCondition"] = "{ path=" + exe + " ; argv[]=" + exe + " ; " + mutation + " }"
		if _, err := canonicalSystemdDefinition(p, m); err == nil {
			t.Fatalf("accepted ambiguous root ExecCondition %q", mutation)
		}
	}
	p, m = rootDefinitionFixture(t)
	m["ExecStart"] += " unparsed"
	if _, err := canonicalSystemdDefinition(p, m); err == nil {
		t.Fatal("accepted unparsed effective execution definition")
	}
	// A root-owned script with an unrelated basename must not bypass loader
	// policy through its shebang. No executable is invoked by this test.
	script, err := filepath.EvalSymlinks("/usr/bin/ldd")
	if err == nil {
		contents, readErr := os.ReadFile(script)
		if readErr == nil && strings.HasPrefix(string(contents), "#!") {
			p, m = rootDefinitionFixture(t)
			p.Target.TrustedFiles = []string{script}
			m["ExecStart"] = "{ path=" + script + " ; argv[]=" + script + " ; ignore_errors=no }"
			if _, err = canonicalSystemdDefinition(p, m); err == nil {
				t.Fatal("root pinned shebang entrypoint bypassed interpreter policy")
			}
		}
	}
}

func TestSecurityClosureFixedProbeOutputAndCancellation(t *testing.T) {
	head, err := filepath.EvalSymlinks("/usr/bin/head")
	if err != nil {
		t.Skip(err)
	}
	ctx, end := context.WithTimeout(context.Background(), time.Second)
	defer end()
	if _, err = fixedCommand(ctx, head, []string{"-c", "65536", "/dev/zero"}, 0, 1024); err == nil {
		t.Fatal("actual pipe capture bypassed stdout cap")
	}
	if b, err := fixedCommand(ctx, head, []string{"-c", "128", "/dev/zero"}, 0, 1024); err != nil || len(b) != 128 {
		t.Fatalf("bounded successful output unavailable: bytes=%d err=%v", len(b), err)
	}
	sleep, err := filepath.EvalSymlinks("/usr/bin/sleep")
	if err != nil {
		t.Skip(err)
	}
	ctx, end2 := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer end2()
	started := time.Now()
	if _, err = fixedCommand(ctx, sleep, []string{"20"}, 0, 1024); err == nil || time.Since(started) > time.Second {
		t.Fatalf("probe process cancellation unbounded: %v", err)
	}
}

func TestSecurityClosureModelEnumerationGlobalBudget(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "nested")
	if err := os.Mkdir(nested, 0700); err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 4120; n++ {
		dir := nested
		if n >= 4096 {
			dir = root
		}
		if err := os.WriteFile(filepath.Join(dir, strconv.Itoa(n)+".gguf"), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	metadata := scanModelFiles([]string{root})
	if metadata.Complete || metadata.Count > 4095 {
		t.Fatalf("recursive batch exceeded global entry budget: %+v", metadata)
	}
	for _, setting := range metadata.settings() {
		if setting.Value != nil || setting.Provenance != "unknown" {
			t.Fatal("truncated inventory advertised complete measurements")
		}
	}
}

func TestSecurityClosureModelMetadataRejectsSymlinkAncestors(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(root, "outside", "weights")
	if err := os.MkdirAll(outside, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "model.gguf"), []byte("synthetic"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Dir(outside), filepath.Join(root, "approved-link")); err != nil {
		t.Fatal(err)
	}
	if got := scanModelFiles([]string{filepath.Join(root, "approved-link", "weights")}); got.Complete || got.Available || got.Count != 0 {
		t.Fatalf("symlink ancestor escaped approved real root: %+v", got)
	}
	if got := scanModelFiles([]string{outside}); !got.Complete || got.Count != 1 {
		t.Fatalf("explicit real approved root unavailable: %+v", got)
	}
}

func TestSecurityClosureQuotedKnownCredentials(t *testing.T) {
	r := &Redactor{}
	for _, key := range []string{"password", "secret", "api_key", "access_token", "authorization"} {
		for _, value := range []string{`"fixture_sensitive_credential"`, `"Bearer fixture sensitive credential"`, `'fixture sensitive credential'`, `"fixture\"quoted credential"`} {
			line := `{"` + key + `":` + value + `}`
			got := r.Clean([]byte(line))
			if strings.Contains(got, "fixture") || !strings.Contains(got, "[redacted]") {
				t.Fatalf("quoted known credential survived: %q", got)
			}
		}
	}
	for _, line := range []string{`password="fixture sensitive credential`, `secret='fixture sensitive credential`, `authorization="fixture sensitive credential\`} {
		if got := r.Clean([]byte(line)); strings.Contains(got, "fixture") {
			t.Fatalf("unterminated credential quote retained secret: %q", got)
		}
	}
	if got := r.Clean([]byte(`{"status":"healthy"}`)); got != `{"status":"healthy"}` {
		t.Fatalf("ordinary structured log changed: %q", got)
	}
}

func TestSecurityClosurePrivateJournalFIFO(t *testing.T) {
	if path := os.Getenv("MONIK_SPARK_FIFO_TEST_PATH"); path != "" {
		if _, err := readPrivateFile(path, 4<<20, uint32(os.Geteuid())); err == nil {
			os.Exit(3)
		}
		os.Exit(0)
	}
	path := filepath.Join(t.TempDir(), "journal.fifo")
	if err := unix.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, end := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer end()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSecurityClosurePrivateJournalFIFO$")
	cmd.Env = append(os.Environ(), "MONIK_SPARK_FIFO_TEST_PATH="+path, "GORACE=atexit_sleep_ms=0")
	if err := cmd.Run(); err != nil {
		t.Fatalf("non-regular private journal blocked or was accepted: %v", err)
	}
}

func TestSecurityClosureDockerMountAuthorityAncestry(t *testing.T) {
	d := frozenClosureDeclaration()
	for _, source := range []string{"/tmp/..", "/var/run/", "/home/../proc", "/run", "/proc/self", "/sys", "/dev", "/var/lib/docker", "/etc/monik-spark"} {
		d.Mounts = []DockerMount{{Type: "bind", Source: source, Target: "/weights", ReadOnly: true}}
		if err := d.validate(); err == nil {
			t.Fatalf("authority mount accepted: %s", source)
		}
	}
	link := filepath.Join(t.TempDir(), "weights")
	if err := os.Symlink("/run", link); err != nil {
		t.Fatal(err)
	}
	d.Mounts = []DockerMount{{Type: "bind", Source: link, Target: "/weights", ReadOnly: true}}
	if err := d.validate(); err == nil {
		t.Fatal("symlink mount authority accepted")
	}
	d.Mounts[0].Source = "/usr/share/zoneinfo"
	if err := d.validate(); err != nil {
		t.Fatalf("protected ordinary data directory rejected: %v", err)
	}
}

func frozenClosureDeclaration() DockerDeclaration {
	return DockerDeclaration{Name: "frozen-fixture", Image: "fixture@sha256:" + strings.Repeat("a", 64), User: "1000", Command: []string{"model"}, Environment: []string{}, Mounts: []DockerMount{}, NetworkMode: "none", StopTimeout: 60}
}

func frozenClosureObject(d DockerDeclaration, safe bool) map[string]any {
	caps, security := []string{"ALL"}, []string{"no-new-privileges:true"}
	if !safe {
		caps, security = nil, nil
	}
	return map[string]any{"Id": strings.Repeat("b", 64), "Image": "sha256:" + strings.Repeat("a", 64), "Config": map[string]any{"Image": d.Image, "User": d.User, "Cmd": d.Command, "Env": d.Environment, "WorkingDir": d.WorkingDir, "Entrypoint": []string{}, "StopTimeout": d.StopTimeout}, "HostConfig": map[string]any{"Mounts": []any{}, "NetworkMode": d.NetworkMode, "CapDrop": caps, "SecurityOpt": security, "RestartPolicy": map[string]string{"Name": "no"}}, "State": map[string]any{"Status": "running", "Running": true, "StartedAt": time.Now().UTC()}}
}

func TestSecurityClosureDockerCreateRechecksExactIDBeforeEffectAndLogs(t *testing.T) {
	for _, safe := range []bool{false, true} {
		t.Run(strconv.FormatBool(safe), func(t *testing.T) {
			d := frozenClosureDeclaration()
			socket := filepath.Join(t.TempDir(), "docker.sock")
			listener, err := net.Listen("unix", socket)
			if err != nil {
				t.Fatal(err)
			}
			var created atomic.Bool
			var starts, logs, exactInspects atomic.Int32
			id := strings.Repeat("b", 64)
			server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == "/containers/create":
					created.Store(true)
					w.WriteHeader(201)
					json.NewEncoder(w).Encode(map[string]string{"Id": id})
				case r.URL.Path == "/containers/"+id+"/json":
					exactInspects.Add(1)
					json.NewEncoder(w).Encode(frozenClosureObject(d, safe))
				case strings.HasSuffix(r.URL.Path, "/json"):
					if !created.Load() {
						w.WriteHeader(404)
					} else {
						json.NewEncoder(w).Encode(frozenClosureObject(d, safe))
					}
				case r.URL.Path == "/containers/"+id+"/start":
					starts.Add(1)
					w.WriteHeader(204)
				case r.URL.Path == "/containers/"+id+"/logs":
					logs.Add(1)
					w.WriteHeader(200)
				default:
					t.Errorf("unexpected Docker API %s %s", r.Method, r.URL.Path)
					w.WriteHeader(400)
				}
			})}
			go server.Serve(listener)
			defer server.Close()
			defer listener.Close()
			p := Profile{Target: Target{Kind: "docker", DockerSocket: socket, Frozen: &d}}
			_, effectErr := dockerEffect(context.Background(), p, "start")
			reader, logErr := dockerLogs(context.Background(), p)
			if reader != nil {
				io.Copy(io.Discard, reader)
				reader.Close()
			}
			if exactInspects.Load() == 0 {
				t.Fatal("created identity was not inspected")
			}
			if safe {
				if effectErr != nil || logErr != nil || starts.Load() != 1 || logs.Load() != 1 {
					t.Fatalf("approved exact ID unavailable: effects=%d logs=%d err=%v/%v", starts.Load(), logs.Load(), effectErr, logErr)
				}
			} else if effectErr == nil || logErr == nil || starts.Load() != 0 || logs.Load() != 0 {
				t.Fatal("unapproved created definition received start/log effects")
			}
		})
	}
}

func TestSecurityClosureAuthenticatedHTTPDelayCannotRenewDispatch(t *testing.T) {
	worker, _, _ := workerFixture(t, true)
	params := protocol.ModelActionParams{ProfileID: "model-a", ProfileRevision: "sha256:" + strings.Repeat("a", 64), ExpectedGeneration: 1, ControlEpoch: "epoch-a", ExpectedAgentSession: worker.session}
	b, _ := json.Marshal(params)
	var raw map[string]any
	json.Unmarshal(b, &raw)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture-credential" {
			t.Error("fixture missing authentication")
		}
		serverTime := time.Now().UTC()
		job := protocol.JobEnvelope{JobID: "delayed-job", OperationID: "delayed-operation", SchemaVersion: 3, ControllerID: "fixture-controller", Action: "model.start", Params: raw, Deadline: serverTime.Add(40 * time.Millisecond)}
		time.Sleep(100 * time.Millisecond)
		json.NewEncoder(w).Encode(protocol.ControlResponse{ControllerID: "fixture-controller", ServerTime: serverTime, Ack: &protocol.IngestAck{Committed: true, UpToSequence: 1}, Features: protocol.SparkFeatures, Jobs: []protocol.JobEnvelope{job}})
	}))
	defer server.Close()
	worker.state.File.ControllerURL = server.URL
	worker.client = server.Client()
	if err := worker.send(context.Background(), protocol.AgentReport{Sequence: 1, IsLive: true}); err != nil {
		t.Fatal(err)
	}
	job := worker.jobs["delayed-job"]
	if job == nil || job.Request != nil || job.Receipt.Status != protocol.TargetExpired {
		t.Fatalf("delayed authenticated response renewed expired dispatch: %+v", job)
	}
}

func TestSecurityClosureDispatchIncludesMutexWaitAndOriginalBudget(t *testing.T) {
	worker, _, _ := workerFixture(t, true)
	worker.features = protocol.SparkFeatures
	params := protocol.ModelActionParams{ProfileID: "model-a", ProfileRevision: "sha256:" + strings.Repeat("a", 64), ExpectedGeneration: 1, ControlEpoch: "epoch-a", ExpectedAgentSession: worker.session}
	b, _ := json.Marshal(params)
	var raw map[string]any
	json.Unmarshal(b, &raw)
	serverTime := time.Now().UTC()
	job := protocol.JobEnvelope{JobID: "mutex-delayed-job", OperationID: "mutex-delayed-operation", SchemaVersion: 3, ControllerID: "fixture-controller", Action: "model.start", Params: raw, Deadline: serverTime.Add(40 * time.Millisecond)}
	started := time.Now()
	worker.mu.Lock()
	done := make(chan struct{})
	go func() { worker.acceptJob(job, serverTime, started); close(done) }()
	time.Sleep(100 * time.Millisecond)
	worker.mu.Unlock()
	<-done
	if current := worker.jobs[job.JobID]; current == nil || current.Request != nil || current.Receipt.Status != protocol.TargetExpired {
		t.Fatalf("journal lock wait renewed lease: %+v", current)
	}
	job.JobID = "oversized-original-budget"
	job.Deadline = serverTime.Add(31 * time.Second)
	worker.acceptJob(job, serverTime, time.Now().Add(-2*time.Second))
	if current := worker.jobs[job.JobID]; current.Request != nil || current.Receipt.Status != protocol.TargetExpired {
		t.Fatalf("elapsed time hid invalid original lease length: %+v", current)
	}
	// A valid remaining budget is conserved in the local monotonic deadline.
	job.JobID = "valid-short-budget"
	job.Deadline = serverTime.Add(time.Second)
	worker.acceptJob(job, serverTime, time.Now().Add(-100*time.Millisecond))
	worker.mu.Lock()
	request := worker.jobs[job.JobID].Request
	worker.mu.Unlock()
	if request == nil || time.Until(request.Deadline) > 910*time.Millisecond || time.Until(request.Deadline) < 500*time.Millisecond {
		t.Fatalf("valid original budget was not conserved: %+v", request)
	}
}

func TestSecurityClosureStopSupersedesLinkedStartingStageAndReplay(t *testing.T) {
	c, f, p := controlFixture(t)
	initial := actionRequest(c, p, "initial-start", "start")
	if got := c.Execute(context.Background(), initial); got.Status != protocol.TargetSucceeded {
		t.Fatal(got)
	}
	parent := actionRequest(c, p, "parent-restart", "restart")
	if got := c.Execute(context.Background(), parent); got.Stage != "restart_stopped" {
		t.Fatal(got)
	}
	f.starting = true
	stage := actionRequest(c, p, "linked-start", "start")
	stage.ParentJobID, stage.OperationID = parent.JobID, parent.OperationID
	if got := c.Execute(context.Background(), stage); got.Stage != "model_starting" {
		t.Fatal(got)
	}
	stop := actionRequest(c, p, "explicit-stop", "stop")
	if got := c.Execute(context.Background(), stop); got.Status != protocol.TargetSucceeded {
		t.Fatal(got)
	}
	if got := c.Execute(context.Background(), stage); got.ErrorCode != "start_cancelled_by_stop" {
		t.Fatalf("linked stage replay not terminal: %+v", got)
	}
	// A delayed successful child receipt must not revive the canceled parent.
	c.mu.Lock()
	delayed := c.journal.Attempts[stage.JobID]
	delayed.Stage = "done"
	delayed.Receipt = c.evidenceReceipt(stage, p, Instance{State: "running", Identity: "late-instance"}, protocol.TargetSucceeded, "model_started")
	c.finishParentLocked(delayed)
	c.mu.Unlock()
	got := c.Execute(context.Background(), parent)
	if got.Status != protocol.TargetFailed || got.Stage != "restart_cancelled_after_stop" || got.ErrorCode != "superseded_by_stop" {
		t.Fatalf("canceled restart revived: %+v", got)
	}
	if f.effects[p.ID+":start"] != 2 || f.effects[p.ID+":stop"] != 2 {
		t.Fatal("linked stage replay repeated an effect")
	}
}

func TestSecurityClosurePendingStartCancellationSurvivesBrokerReopen(t *testing.T) {
	c, f, p := controlFixture(t)
	p.Weights = []string{"/synthetic/private-fixture/weights"}
	p.Revision = ProfileRevision(p)
	c.Policy.Profiles[0] = p
	c.journal.PolicyHash = digest(c.Policy)
	entered, release := make(chan struct{}), make(chan struct{})
	c.scanWeights = func([]string) modelFileMetadata {
		close(entered)
		<-release
		return modelFileMetadata{Complete: true, Available: true}
	}
	original := actionRequest(c, p, "cancel-before-crash", "start")
	result := make(chan protocol.JobReceipt, 1)
	go func() { result <- c.Execute(context.Background(), original) }()
	<-entered
	defer func() { close(release); <-result }()
	stop := actionRequest(c, p, "durable-safety-stop", "stop")
	if got := c.Execute(context.Background(), stop); got.Status != protocol.TargetSucceeded {
		t.Fatal(got)
	}
	reopened, err := OpenCoordinator(c.Policy, f)
	if err != nil {
		t.Fatal(err)
	}
	if err = reopened.Bind(c.Policy.AgentID, c.Policy.ControllerID, c.session); err != nil {
		t.Fatal(err)
	}
	reopened.scanWeights = func([]string) modelFileMetadata { return modelFileMetadata{Complete: true, Available: true} }
	if replay := reopened.Execute(context.Background(), original); replay.ErrorCode != "start_cancelled_by_stop" || f.effects[p.ID+":start"] != 0 {
		t.Fatalf("durable stop lost preflight cancellation: %+v effects=%v", replay, f.effects)
	}
}

type closureDefinitionDrift struct {
	*fakeSupervisor
	drift atomic.Bool
}

func (s *closureDefinitionDrift) Definition(_ context.Context, p Profile) (string, error) {
	if s.drift.Load() {
		return "sha256:changed-after-initial-preflight", nil
	}
	return p.Target.DefinitionHash, nil
}

func TestSecurityClosureDefinitionDriftDuringWeightsCannotReceiveEffect(t *testing.T) {
	c, f, p := controlFixture(t)
	p.Weights = []string{"/synthetic/private-fixture/weights"}
	p.Revision = ProfileRevision(p)
	c.Policy.Profiles[0] = p
	supervisor := &closureDefinitionDrift{fakeSupervisor: f}
	c.supervisor = supervisor
	c.scanWeights = func([]string) modelFileMetadata {
		supervisor.drift.Store(true)
		return modelFileMetadata{Complete: true, Available: true}
	}
	got := c.Execute(context.Background(), actionRequest(c, p, "drift-during-preflight", "start"))
	if got.ErrorCode != "effective_definition_drift" || f.effects[p.ID+":start"] != 0 {
		t.Fatalf("drifted definition received effect: %+v effects=%v", got, f.effects)
	}
}

func TestSecurityClosureSystemdPreservesAllRepeatedEffectiveArrayEntries(t *testing.T) {
	p, m := rootDefinitionFixture(t)
	exe := p.Target.TrustedFiles[0]
	properties, err := parseSystemdProperties([]byte("ExecStartPre={ path=/owner/mutable ; argv[]=/owner/mutable ; ignore_errors=no }\nExecStartPre={ path=" + exe + " ; argv[]=" + exe + " ; ignore_errors=no }\nEnvironmentFiles=/owner/mutable (ignore_errors=no)\nEnvironmentFiles=" + exe + " (ignore_errors=no)\n"))
	if err != nil || !strings.Contains(properties["ExecStartPre"], "/owner/mutable") || !strings.Contains(properties["EnvironmentFiles"], "/owner/mutable") {
		t.Fatalf("early effective array entries lost: %+v %v", properties, err)
	}
	for key, value := range properties {
		m[key] = value
	}
	if _, err = canonicalSystemdDefinition(p, m); err == nil {
		t.Fatal("early unsafe command/environment source hidden by later safe entry")
	}
	p, m = rootDefinitionFixture(t)
	m["EnvironmentFiles"] = exe + " (ignore_errors=no) /owner/a b (ignore_errors=no)"
	if _, err = canonicalSystemdDefinition(p, m); err == nil {
		t.Fatal("unparsed environment source tail accepted")
	}
	p, m = rootDefinitionFixture(t)
	properties, err = parseSystemdProperties([]byte("ExecStartPre=" + m["ExecStart"] + "\nExecStartPre=" + m["ExecStart"] + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	m["ExecStartPre"] = properties["ExecStartPre"]
	if _, err = canonicalSystemdDefinition(p, m); err != nil {
		t.Fatalf("safe repeated pinned commands rejected: %v", err)
	}
	if _, err = parseSystemdProperties([]byte("User=root\nUser=owner\n")); err == nil {
		t.Fatal("ambiguous scalar identity accepted")
	}
}
