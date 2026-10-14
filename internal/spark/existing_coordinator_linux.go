//go:build linux

package spark

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// No user site, PYTHONPATH, .pth processing, ambient environment, or script-path
// reopen. All executable/imported project bytes originate in sealed, hash-pinned
// descriptors held by this call. __file__ retains the fixed original identity
// needed by the backend's own approval/runtime fencing.
const existingBootstrap = `import json,os,sys,types
try:
 if not sys.flags.isolated or not sys.flags.no_site or os.geteuid()!=0:
  raise ValueError('isolated root required')
 metadata_fd=int(sys.argv[1])
 paths=json.loads(os.read(metadata_fd,8192))
 if len(paths) not in (9,11) or metadata_fd!=3+len(paths):
  raise ValueError('complete fixed source descriptors required')
 def load(name,fd,path):
  module=types.ModuleType(name);module.__file__=path;sys.modules[name]=module
  with os.fdopen(os.dup(fd),'rb') as stream: code=stream.read(2097153)
  if len(code)>2097152:raise ValueError('source size')
  exec(compile(code,path,'exec'),module.__dict__)
  return module
 bridge=load('spark_v4_monik_bridge',4,paths[1])
 model=load('spark_v4_bridge_modelctl',5,paths[2])
 legacy=load('spark_fixed_legacy_runtime',10,paths[7])
 for key in ('Refusal','ObservationError','protect','unit_name','proc_birth','PROJECT','MANIFEST'):
  setattr(legacy,key,getattr(model,key))
 model._LEGACY_SUPPORT=legacy
 with os.fdopen(os.dup(7),'rb') as stream:manifest=model.validate_manifest(json.load(stream,object_pairs_hook=bridge.strict_object))
 body=bridge.decode_envelope(sys.stdin.buffer.read(8193))
 controller=model.Controller(manifest,model.SystemAdapter(manifest))
 result=bridge.dispatch(controller,body)
 encoded=json.dumps(result,sort_keys=True,separators=(',',':'),allow_nan=False).encode()
 if len(encoded)>131072:raise ValueError('output size')
 sys.stdout.buffer.write(encoded+b'\n')
except Exception as exc:
 sys.stdout.write(json.dumps({'schema_version':1,'error':'typed_bridge_'+type(exc).__name__.lower()})+'\n')
 sys.exit(1)
`

// Open every component relative to its held parent directory. A replacement or
// symlink cannot redirect the read. The root-only source policy is rechecked via
// fstat; seals then make the exact bytes immutable until the child exits.
func existingPinnedFile(pin ExistingCodePin, owner uint32, executable bool) (*os.File, error) {
	if !filepath.IsAbs(pin.Path) || filepath.Clean(pin.Path) != pin.Path || !bareHash(pin.SHA256) {
		return nil, fmt.Errorf("invalid pinned source")
	}
	fd, e := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if e != nil {
		return nil, e
	}
	defer func() {
		if fd >= 0 {
			_ = unix.Close(fd)
		}
	}()
	parts := strings.Split(strings.TrimPrefix(pin.Path, "/"), "/")
	for index, part := range parts {
		var st unix.Stat_t
		if unix.Fstat(fd, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFDIR || st.Uid != 0 && st.Uid != owner || st.Mode&0022 != 0 {
			return nil, fmt.Errorf("protected source ancestor mismatch")
		}
		flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_NONBLOCK | unix.O_CLOEXEC
		if index < len(parts)-1 {
			flags |= unix.O_DIRECTORY
		}
		next, err := unix.Openat(fd, part, flags, 0)
		if err != nil {
			return nil, fmt.Errorf("protected source open failed")
		}
		_ = unix.Close(fd)
		fd = next
	}
	var before, after unix.Stat_t
	limit := int64(2 << 20)
	if executable {
		limit = 64 << 20
	}
	if unix.Fstat(fd, &before) != nil || before.Mode&unix.S_IFMT != unix.S_IFREG || before.Uid != owner || before.Mode&0022 != 0 || before.Nlink != 1 || before.Size < 1 || before.Size > limit {
		return nil, fmt.Errorf("protected source owner/type/size mismatch")
	}
	readFD := fd
	original := os.NewFile(uintptr(readFD), pin.Path)
	fd = -1 // original now owns the descriptor; never close a reused number twice.
	defer original.Close()
	raw, e := io.ReadAll(io.LimitReader(original, limit+1))
	if e != nil || int64(len(raw)) > limit || unix.Fstat(readFD, &after) != nil || before.Dev != after.Dev || before.Ino != after.Ino || before.Size != after.Size || before.Mtim != after.Mtim || before.Ctim != after.Ctim || before.Uid != after.Uid || before.Mode != after.Mode || before.Nlink != after.Nlink {
		return nil, fmt.Errorf("protected source changed during read")
	}
	// Stat_t includes atime, changed by our own read on some filesystems.
	// Rechecking all identity/content fields separately below tolerates only atime.
	hash := sha256.Sum256(raw)
	if hex.EncodeToString(hash[:]) != pin.SHA256 || executable && (len(raw) < 4 || string(raw[:4]) != "\x7fELF" || before.Mode&0111 == 0) {
		return nil, fmt.Errorf("protected source hash/type mismatch")
	}
	sealed, e := existingSealedBytes(raw, executable)
	if e != nil {
		return nil, e
	}
	return sealed, nil
}
func existingSealedBytes(raw []byte, executable bool) (*os.File, error) {
	fd, e := unix.MemfdCreate("monik-existing-pinned", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING)
	if e != nil {
		return nil, e
	}
	f := os.NewFile(uintptr(fd), "monik-existing-pinned")
	fail := func(err error) (*os.File, error) { _ = f.Close(); return nil, err }
	if _, e = f.Write(raw); e != nil {
		return fail(e)
	}
	if executable {
		if e = unix.Fchmod(fd, 0500); e != nil {
			return fail(e)
		}
	}
	if _, e = f.Seek(0, io.SeekStart); e != nil {
		return fail(e)
	}
	if _, e = unix.FcntlInt(uintptr(fd), unix.F_ADD_SEALS, unix.F_SEAL_WRITE|unix.F_SEAL_GROW|unix.F_SEAL_SHRINK|unix.F_SEAL_SEAL); e != nil {
		return fail(e)
	}
	return f, nil
}
func existingCall(ctx context.Context, p Profile, envelope existingEnvelope) (existingResponse, error) {
	return existingProfilesCall(ctx, []Profile{p}, envelope)
}

func existingProfilesCall(ctx context.Context, profiles []Profile, envelope existingEnvelope) (existingResponse, error) {
	var result existingResponse
	if os.Geteuid() != 0 || validateExistingProfiles(profiles, envelope) != nil {
		return result, fmt.Errorf("root protected existing coordinator required")
	}
	p := profiles[0]
	input, e := json.Marshal(envelope)
	if e != nil || len(input) > 8192 {
		return result, fmt.Errorf("typed input bound")
	}
	files := make([]*os.File, 0, 8)
	defer func() {
		for _, f := range files {
			_ = f.Close()
		}
	}()
	for index, pin := range p.Target.Existing.CodePins {
		f, err := existingPinnedFile(pin, 0, index == 0)
		if err != nil {
			return result, err
		}
		files = append(files, f)
	}
	// All members share the same fixed executable/imported bytes, but their
	// individual units/drop-ins remain separately approved and freshly pinned.
	// Additional files are held locally; the sealed loader's fixed descriptor
	// layout and import boundary are unchanged.
	seen := make(map[string]bool)
	for _, pin := range p.Target.Existing.CodePins {
		seen[pin.Path] = true
	}
	for _, profile := range profiles[1:] {
		for _, pin := range profile.Target.Existing.CodePins {
			if seen[pin.Path] {
				continue
			}
			held, err := existingPinnedFile(pin, 0, false)
			if err != nil {
				return result, err
			}
			defer held.Close()
			seen[pin.Path] = true
		}
	}
	metadata, _ := json.Marshal(existingRequiredPaths(p))
	f, e := existingSealedBytes(metadata, false)
	if e != nil {
		return result, e
	}
	files = append(files, f)
	// The metadata descriptor is derived solely from the validated fixed pin
	// list; callers cannot select descriptors or imported source paths.
	cmd := exec.CommandContext(ctx, "/proc/self/fd/3", "-I", "-S", "-B", "-c", existingBootstrap, strconv.Itoa(3+len(p.Target.Existing.CodePins)))
	cmd.ExtraFiles = files
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C.UTF-8", "LC_ALL=C.UTF-8", "HOME=/root"}
	cmd.Dir = "/"
	cmd.Stdin = bytes.NewReader(input)
	out := &boundedWriter{limit: 131072}
	cmd.Stdout = out
	cmd.Stderr = io.Discard
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = 200 * time.Millisecond
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	runErr := cmd.Run()
	if out.exceeded || decodeExistingProfilesResponse(out.Bytes(), envelope, profiles, &result) != nil || runErr != nil {
		return existingResponse{}, fmt.Errorf("typed existing coordinator call unconfirmed")
	}
	return result, nil
}

func validateExistingProfiles(profiles []Profile, envelope existingEnvelope) error {
	if len(profiles) == 0 || len(profiles) > len(existingIDs) || len(profiles) != 1 && envelope.Method != "observe_many" {
		return fmt.Errorf("invalid existing profile batch")
	}
	seenIDs, pins := map[string]bool{}, map[string]string{}
	for index, p := range profiles {
		if p.validateExistingCoordinator() != nil || seenIDs[p.ID] {
			return fmt.Errorf("invalid existing profile approval")
		}
		seenIDs[p.ID] = true
		if envelope.Method == "observe_many" && (len(envelope.ProfileIDs) != len(profiles) || envelope.ProfileIDs[index] != p.ID) {
			return fmt.Errorf("existing profile selectors differ")
		}
		if p.Target.Existing.PythonPath != profiles[0].Target.Existing.PythonPath {
			return fmt.Errorf("existing Python approval differs")
		}
		for _, pin := range p.Target.Existing.CodePins {
			if prior, ok := pins[pin.Path]; ok && prior != pin.SHA256 {
				return fmt.Errorf("existing shared source approval differs")
			}
			pins[pin.Path] = pin.SHA256
		}
	}
	return nil
}

func decodeExistingResponse(raw []byte, envelope existingEnvelope, p Profile, result *existingResponse) error {
	return decodeExistingProfilesResponse(raw, envelope, []Profile{p}, result)
}

func decodeExistingProfilesResponse(raw []byte, envelope existingEnvelope, profiles []Profile, result *existingResponse) error {
	*result = existingResponse{}
	if len(profiles) == 0 || len(profiles) > len(existingIDs) || len(raw) > 131072 || strictDecode(raw, result) != nil || result.SchemaVersion != 1 || result.Error != "" {
		return fmt.Errorf("invalid typed response")
	}
	switch envelope.Method {
	case "observe_many":
		if result.Receipt != nil || result.Authority != nil || len(result.Models) != len(profiles) || len(envelope.ProfileIDs) != len(profiles) {
			return fmt.Errorf("typed observation invalid")
		}
		seen := map[string]bool{}
		for index, profile := range profiles {
			if envelope.ProfileIDs[index] != profile.ID || seen[profile.ID] || validateExistingSnapshot(result.Models[index], profile) != nil || len(profiles) > 1 && result.Models[index].ProfileRevision != profile.Target.Existing.BackendRevision {
				return fmt.Errorf("typed observation member invalid")
			}
			seen[profile.ID] = true
		}
	case "bind_current_agent":
		if len(result.Models) != 0 || result.Receipt != nil || result.Authority == nil || *result.Authority != envelope.Binding.existingAuthority {
			return fmt.Errorf("typed binding invalid")
		}
	default:
		if len(result.Models) != 0 || result.Authority != nil || result.Receipt == nil || validateExistingReceipt(*result.Receipt, *envelope.Request) != nil {
			return fmt.Errorf("typed receipt invalid")
		}
	}
	var top map[string]json.RawMessage
	_ = json.Unmarshal(raw, &top)
	if len(top) != 2 || requireExistingObject(raw, []string{"schema_version"}, nil, []string{"models", "receipt", "authority"}) != nil {
		return fmt.Errorf("ambiguous typed envelope")
	}
	switch envelope.Method {
	case "observe_many":
		var models []json.RawMessage
		_ = json.Unmarshal(top["models"], &models)
		for _, model := range models {
			if requireExistingRuntime(model, true) != nil {
				return fmt.Errorf("incomplete typed snapshot")
			}
			var object map[string]json.RawMessage
			_ = json.Unmarshal(model, &object)
			if !bytes.Equal(object["instance"], []byte("null")) && requireExistingPhysical(object["instance"]) != nil {
				return fmt.Errorf("incomplete physical instance")
			}
		}
	case "bind_current_agent":
		if requireExistingObject(top["authority"], []string{"control_epoch", "agent_session", "boot_id"}, nil, nil) != nil {
			return fmt.Errorf("incomplete typed authority")
		}
	default:
		if requireExistingObject(top["receipt"], []string{"operation_id", "profile_id", "action", "phase", "run_id", "generation", "attempt"}, []string{"run_id", "attempt"}, []string{"result", "stage"}) != nil {
			return fmt.Errorf("incomplete typed receipt")
		}
		var receipt map[string]json.RawMessage
		_ = json.Unmarshal(top["receipt"], &receipt)
		if resultRaw, present := receipt["result"]; present {
			var value map[string]json.RawMessage
			if json.Unmarshal(resultRaw, &value) != nil || value == nil {
				return fmt.Errorf("result object required")
			}
			_, runtime := value["model_id"]
			if _, state := value["state"]; state {
				runtime = true
			}
			if _, active := value["process_active"]; active {
				runtime = true
			}
			if _, ready := value["api_ready"]; ready {
				runtime = true
			}
			if runtime {
				if requireExistingRuntime(resultRaw, false) != nil {
					return fmt.Errorf("incomplete typed runtime result")
				}
			} else {
				if requireExistingObject(resultRaw, []string{"reason"}, nil, []string{"effect_invoked"}) != nil {
					return fmt.Errorf("invalid typed non-runtime result")
				}
				reason := result.Receipt.Result.Reason
				if !member(reason, "authority_replaced_before_effect", "dispatch_expired", "original_runtime_replaced_or_missing") || reason == "original_runtime_replaced_or_missing" && (len(value) != 1 || result.Receipt.Phase != "unresolved") || reason != "original_runtime_replaced_or_missing" && (result.Receipt.Result.EffectInvoked == nil || *result.Receipt.Result.EffectInvoked) {
					return fmt.Errorf("invalid typed refusal result")
				}
			}
		} else if result.Receipt.Phase == "completed" {
			return fmt.Errorf("completed result required")
		}
	}
	return nil
}
func requireExistingObject(raw json.RawMessage, required, nullable, optional []string) error {
	var value map[string]json.RawMessage
	if json.Unmarshal(raw, &value) != nil || value == nil {
		return fmt.Errorf("typed object required")
	}
	for _, field := range required {
		item, present := value[field]
		if !present || bytes.Equal(item, []byte("null")) && !member(field, nullable...) {
			return fmt.Errorf("required non-null typed field missing")
		}
	}
	for field, item := range value {
		if !member(field, required...) && !member(field, optional...) {
			return fmt.Errorf("unknown typed shape field")
		}
		if member(field, optional...) && bytes.Equal(item, []byte("null")) && !member(field, nullable...) {
			return fmt.Errorf("optional typed field cannot be null")
		}
	}
	return nil
}
func requireExistingPhysical(raw json.RawMessage) error {
	if requireExistingObject(raw, []string{"run_id", "owner", "container_id", "container_created", "engine_pid", "engine_birth"}, []string{"owner", "container_id", "container_created", "engine_pid", "engine_birth"}, []string{"legacy_declaration_sha256"}) != nil {
		return fmt.Errorf("physical instance fields required")
	}
	var value map[string]json.RawMessage
	_ = json.Unmarshal(raw, &value)
	if !bytes.Equal(value["owner"], []byte("null")) && requireExistingObject(value["owner"], []string{"unit", "invocation_id", "pid", "birth"}, nil, nil) != nil {
		return fmt.Errorf("owner identity fields required")
	}
	return nil
}
func requireExistingRuntime(raw json.RawMessage, observation bool) error {
	required := []string{"model_id", "state", "process_active", "api_ready", "context", "endpoint", "engine_log", "action_log", "message", "generation", "attempt", "run_id"}
	nullable := []string{"context", "attempt", "run_id"}
	optional := []string{"shutdown_verified", "shutdown_witness", "instance_identity"}
	if observation {
		required = append(required, "profile_revision", "instance_identity", "observed_at_ms", "instance")
		nullable = append(nullable, "instance")
		optional = append(optional, "control_epoch", "agent_session", "boot_id")
	}
	if requireExistingObject(raw, required, nullable, optional) != nil {
		return fmt.Errorf("runtime fields required")
	}
	var object map[string]json.RawMessage
	_ = json.Unmarshal(raw, &object)
	if witness, present := object["shutdown_witness"]; present {
		if requireExistingObject(witness, []string{"kind", "model_id", "run_id", "generation", "attempt", "instance_identity", "owner", "container_id", "container_created", "engine_pid", "engine_birth"}, []string{"run_id", "attempt", "owner", "container_id", "container_created", "engine_pid", "engine_birth"}, []string{"legacy_declaration_sha256"}) != nil {
			return fmt.Errorf("complete physical shutdown witness required")
		}
		var values map[string]json.RawMessage
		_ = json.Unmarshal(witness, &values)
		if !bytes.Equal(values["owner"], []byte("null")) && requireExistingObject(values["owner"], []string{"unit", "invocation_id", "pid", "birth"}, nil, nil) != nil {
			return fmt.Errorf("complete shutdown owner required")
		}
	}
	return nil
}

func validateExistingSnapshot(s existingSnapshot, p Profile) error {
	generation := int64(-1)
	if s.Generation != nil {
		generation = *s.Generation
	}
	now := time.Now().UnixMilli()
	if s.Instance != nil && !validExistingDeclaration(p.ID, s.Instance.LegacyDeclarationSHA256) {
		return fmt.Errorf("invalid fixed legacy declaration witness")
	}
	if s.ModelID != p.ID || !bareHash(s.ProfileRevision) || s.InstanceIdentity != "" && !bareHash(s.InstanceIdentity) || generation < 0 || generation > 9007199254740991 || s.ObservedAtMS < now-5000 || s.ObservedAtMS > now+1000 || !member(s.State, "READY", "STARTING", "STOPPING", "STOPPED", "BLOCKED", "FAILED", "UNKNOWN") || s.APIReady && (s.State != "READY" || !s.ProcessActive || !existingReadyPhysical(s, p)) || s.RunID != nil && !lowerHex(*s.RunID, 32) || s.Instance == nil && (s.RunID != nil || s.Attempt != nil || s.InstanceIdentity != "") || s.Instance != nil && (!equalString(s.Instance.RunID, s.RunID) || s.RunID == nil || s.Attempt == nil || !lowerHex(*s.Attempt, 32) || s.InstanceIdentity != existingPhysicalIdentity(s.Instance)) {
		return fmt.Errorf("invalid bounded existing runtime witness")
	}
	return nil
}
func (LocalSupervisor) ExistingObserve(ctx context.Context, p Profile) (existingSnapshot, error) {
	response, e := existingCall(ctx, p, existingEnvelope{SchemaVersion: 1, Method: "observe_many", ProfileIDs: []string{p.ID}})
	if e != nil {
		return existingSnapshot{}, e
	}
	return response.Models[0], nil
}

func (LocalSupervisor) ExistingObserveMany(ctx context.Context, profiles []Profile) ([]existingSnapshot, error) {
	ids := make([]string, len(profiles))
	for index, p := range profiles {
		ids[index] = p.ID
	}
	response, err := existingProfilesCall(ctx, profiles, existingEnvelope{SchemaVersion: 1, Method: "observe_many", ProfileIDs: ids})
	return response.Models, err
}
func (LocalSupervisor) ExistingBind(ctx context.Context, p Profile, b existingBinding) error {
	_, e := existingCall(ctx, p, existingEnvelope{SchemaVersion: 1, Method: "bind_current_agent", Binding: &b})
	return e
}
func (LocalSupervisor) ExistingSubmit(ctx context.Context, p Profile, q existingRequest) (existingReceipt, error) {
	response, e := existingCall(ctx, p, existingEnvelope{SchemaVersion: 1, Method: "submit", Request: &q})
	if e != nil {
		return existingReceipt{}, e
	}
	return *response.Receipt, nil
}
func (LocalSupervisor) ExistingReceipt(ctx context.Context, p Profile, q existingRequest, a existingAuthority) (existingReceipt, error) {
	response, e := existingCall(ctx, p, existingEnvelope{SchemaVersion: 1, Method: "receipt", Request: &q, Authority: &a})
	if e != nil {
		return existingReceipt{}, e
	}
	return *response.Receipt, nil
}
