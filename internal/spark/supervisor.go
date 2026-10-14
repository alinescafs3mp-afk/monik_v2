package spark

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Instance struct {
	State            string            `json:"state"`
	Identity         string            `json:"identity"`
	PID              int               `json:"pid,omitempty"`
	StartedAt        time.Time         `json:"started_at,omitempty"`
	DefinitionHash   string            `json:"definition_hash"`
	Available        bool              `json:"available"`
	ShutdownVerified bool              `json:"shutdown_verified,omitempty"`
	Backend          *existingSnapshot `json:"backend,omitempty"`
}
type Supervisor interface {
	Inspect(context.Context, Profile) (Instance, error)
	Effect(context.Context, Profile, string) (Instance, error)
	Definition(context.Context, Profile) (string, error)
	Logs(context.Context, Profile) (LogReader, error)
}
type LocalSupervisor struct{}

func (s LocalSupervisor) Inspect(ctx context.Context, p Profile) (Instance, error) {
	if p.Target.Kind == existingCoordinatorKind {
		snap, err := s.ExistingObserve(ctx, p)
		return snap.instance(p), err
	}
	if p.Target.Kind == configuredCatalogKind {
		return configuredCatalogInspect(p)
	}
	if p.Target.Kind == "docker" {
		return dockerInspect(ctx, p)
	}
	return systemdInspect(ctx, p)
}
func (s LocalSupervisor) Definition(ctx context.Context, p Profile) (string, error) {
	if p.Target.Kind == existingCoordinatorKind {
		snap, err := s.ExistingObserve(ctx, p)
		if err != nil || snap.ProfileRevision != p.Target.Existing.BackendRevision {
			return "", fmt.Errorf("existing coordinator approval drift")
		}
		return p.Target.DefinitionHash, nil
	}
	if p.Target.Kind == configuredCatalogKind {
		return configuredCatalogDefinition(p)
	}
	if p.ObservationOnly {
		if err := p.validateObservationOnly(); err != nil {
			return "", err
		}
	}
	if p.Target.Kind == "docker" {
		return dockerDefinition(ctx, p)
	}
	return systemdDefinition(ctx, p)
}
func (s LocalSupervisor) Effect(ctx context.Context, p Profile, action string) (Instance, error) {
	if p.Target.Kind == existingCoordinatorKind {
		return Instance{}, fmt.Errorf("existing coordinator requires a complete typed action request")
	}
	if p.ObservationOnly || p.Target.Kind == configuredCatalogKind {
		return Instance{}, fmt.Errorf("observation-only profile cannot execute supervisor effects")
	}
	if !member(action, "start", "stop") {
		return Instance{}, fmt.Errorf("unsupported supervisor effect")
	}
	if p.Target.Kind == "docker" {
		return dockerEffect(ctx, p, action)
	}
	definition, err := s.Definition(ctx, p)
	if err != nil || definition != p.Target.DefinitionHash {
		return Instance{}, fmt.Errorf("approved unit effective definition drift")
	}
	args := systemdArgs(p)
	args = append(args, "--no-block", action, "--", p.Target.Unit)
	if _, e := fixedCommand(ctx, "/usr/bin/systemctl", args, systemdUID(p), 16<<10); e != nil {
		return Instance{}, e
	}
	return systemdInspect(ctx, p)
}
func systemdArgs(p Profile) []string {
	if p.Target.Kind == "systemd-user" {
		return []string{"--user"}
	}
	return nil
}
func systemdUID(p Profile) uint32 {
	if p.Target.Kind == "systemd-user" {
		return p.Target.UserUID
	}
	return 0
}
func systemdShow(ctx context.Context, p Profile, props string) (map[string]string, error) {
	args := append(systemdArgs(p), "show", "--no-pager", "--property="+props, "--", p.Target.Unit)
	b, e := fixedCommand(ctx, "/usr/bin/systemctl", args, systemdUID(p), 128<<10)
	if e != nil {
		return nil, e
	}
	m, e := parseSystemdProperties(b)
	if e != nil {
		return nil, e
	}
	if m["LoadState"] != "" && m["LoadState"] != "loaded" {
		return nil, fmt.Errorf("approved unit not loaded")
	}
	return m, nil
}

func parseSystemdProperties(b []byte) (map[string]string, error) {
	m := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		if line == "" {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("unit properties malformed")
		}
		if before, exists := m[k]; exists {
			// systemctl emits array elements as repeated property lines. Every
			// effective command/environment file must remain in the binding.
			if !member(k, "EnvironmentFiles", "ExecCondition", "ExecConditionEx", "ExecStart", "ExecStartPre", "ExecStartPost", "ExecStop", "ExecStopPost", "ExecStartEx", "ExecStartPreEx", "ExecStartPostEx", "ExecStopEx", "ExecStopPostEx") {
				return nil, fmt.Errorf("unit scalar properties ambiguous")
			}
			m[k] = strings.TrimSpace(before + " " + v)
		} else {
			m[k] = v
		}
	}
	return m, nil
}
func systemdDefinition(ctx context.Context, p Profile) (string, error) {
	m, e := systemdShow(ctx, p, "LoadState,FragmentPath,DropInPaths,User,Group,ExecCondition,ExecConditionEx,ExecStart,ExecStartPre,ExecStartPost,ExecStop,ExecStopPost,ExecStartEx,ExecStartPreEx,ExecStartPostEx,ExecStopEx,ExecStopPostEx,Environment,EnvironmentFiles,PassEnvironment,WorkingDirectory,RootDirectory,RootImage,BindPaths,BindReadOnlyPaths,TemporaryFileSystem,MountImages,ExtensionImages,ExtensionDirectories,JoinsNamespaceOf,KillMode,TimeoutStopUSec,SendSIGKILL,Requires,PartOf,BindsTo")
	if e != nil {
		return "", e
	}
	return canonicalSystemdDefinition(p, m)
}
func canonicalSystemdDefinition(p Profile, m map[string]string) (string, error) {
	if p.ObservationOnly {
		return canonicalSystemdObservation(p, m)
	}
	canonicalSystemdDependencySets(m)
	var e error
	for _, field := range []string{"RootDirectory", "RootImage", "BindPaths", "BindReadOnlyPaths", "TemporaryFileSystem", "MountImages", "ExtensionImages", "ExtensionDirectories", "JoinsNamespaceOf"} {
		if strings.TrimSpace(m[field]) != "" {
			return "", fmt.Errorf("rebased service execution filesystem not supported")
		}
	}
	if m["FragmentPath"] == "" {
		return "", fmt.Errorf("transient/unbound unit not approved")
	}
	owner := uint32(0)
	if p.Target.Kind == "systemd-user" {
		owner = p.Target.UserUID
	}
	// User-manager definitions can be non-root, but never execute as root.
	rootRuntime := false
	if owner == 0 {
		var uid uint64
		user := m["User"]
		if user == "" {
			uid = 0
		} else if n, err := strconv.ParseUint(user, 10, 32); err == nil {
			uid = n
		} else {
			passwd, err := readBoundedFile("/etc/passwd", 1<<20)
			if err != nil {
				return "", fmt.Errorf("effective model account unavailable")
			}
			found := false
			for _, line := range strings.Split(string(passwd), "\n") {
				f := strings.Split(line, ":")
				if len(f) > 2 && f[0] == user {
					uid, e = strconv.ParseUint(f[2], 10, 32)
					found = e == nil
					break
				}
			}
			if !found {
				return "", fmt.Errorf("effective model account not locally verified")
			}
		}
		rootRuntime = uid == 0
	}
	if rootRuntime && (!p.Target.AllowRootUnit || len(p.Target.TrustedFiles) == 0) {
		return "", fmt.Errorf("root model unit requires explicit protected launcher approval")
	}
	for _, path := range append([]string{m["FragmentPath"]}, strings.Fields(m["DropInPaths"])...) {
		if e = trustedPath(path, owner, false); e != nil {
			return "", e
		}
		hash, err := protectedCodeHash(path, owner)
		if err != nil {
			return "", err
		}
		m["definition_file:"+path] = hash
		contents, err := readBoundedFile(path, 1<<20)
		if err != nil || !unitPrivilegeSyntaxSafe(string(contents)) {
			return "", fmt.Errorf("unit privileged/shell prefix not supported")
		}
	}
	pinned := map[string]bool{}
	if rootRuntime && unsafeRootEnvironment(m["Environment"]) {
		return "", fmt.Errorf("root unit loader environment unsafe")
	}
	if rootRuntime && strings.TrimSpace(m["PassEnvironment"]) != "" {
		return "", fmt.Errorf("root ambient manager environment not approved")
	}
	for _, path := range p.Target.TrustedFiles {
		if e = trustedPath(path, 0, false); e != nil {
			return "", e
		}
		hash, err := protectedCodeHash(path, 0)
		if err != nil {
			return "", err
		}
		m["trusted:"+path] = hash
		pinned[path] = true
	}
	// Explicitly frozen script/code/environment files are required; mere
	// ownership of the .service does not approve writable executable content.
	for _, field := range []string{"ExecCondition", "ExecConditionEx", "ExecStart", "ExecStartPre", "ExecStartPost", "ExecStop", "ExecStopPost", "ExecStartEx", "ExecStartPreEx", "ExecStartPostEx", "ExecStopEx", "ExecStopPostEx"} {
		configured, e := canonicalExec(m[field])
		if e != nil {
			return "", e
		}
		m[field] = configured
		for _, entry := range regexp.MustCompile(`\{ ([^}]*)\}`).FindAllStringSubmatch(configured, -1) {
			match := regexp.MustCompile(`(?:^|[ {;])path=([^ ;}]+)`).FindStringSubmatch(entry[1])
			path := match[1]
			if e = trustedPath(path, 0, false); e != nil {
				return "", fmt.Errorf("unit executable binding unsafe")
			}
			if rootRuntime && !pinned[path] {
				return "", fmt.Errorf("every root unit executable must be explicitly pinned")
			}
			argvMatches := regexp.MustCompile(`argv\[\]=([^;}]*)`).FindAllStringSubmatch(entry[1], -1)
			if rootRuntime {
				args := strings.Fields(argvMatches[0][1])
				if len(args) == 0 || args[0] != path || strings.ContainsAny(argvMatches[0][1], "\\\"'\r\n") {
					return "", fmt.Errorf("root executable argv binding unverifiable")
				}
				if !supportedRootCommand(path, args) {
					return "", fmt.Errorf("root executable/argv unsupported; use a designated non-root supervisor or exact approved Docker binding")
				}
			}
			if rootRuntime {
				// A pinned script can introduce an unbound shebang interpreter.
				// Root entrypoints are native ELF; supported interpreters below
				// must be invoked explicitly with a protected pinned code file.
				file, err := os.Open(path)
				if err != nil {
					return "", fmt.Errorf("root entrypoint type unavailable")
				}
				var magic [4]byte
				_, err = file.Read(magic[:])
				file.Close()
				if err != nil || string(magic[:]) != "\x7fELF" {
					return "", fmt.Errorf("root entrypoint must be a protected native ELF")
				}
			}
			// Shell/interpreter arguments must be exact protected local files.
			// Root shell -c/eval payloads are rejected rather than pretending to
			// implement a safe general shell parser.
			base := filepath.Base(path)
			kind := interpreterKind(base)
			interpreted := kind != ""
			if interpreted {
				if rootRuntime && !member(kind, "shell", "python") {
					return "", fmt.Errorf("root code loader not supported; use a designated non-root account")
				}
				if rootRuntime && m["WorkingDirectory"] != "" {
					if trustedPath(m["WorkingDirectory"], 0, true) != nil {
						return "", fmt.Errorf("root interpreter working directory must be protected")
					}
				}
				for _, argv := range argvMatches {
					args := strings.Fields(argv[1])
					scriptSeen := false
					for index, arg := range args {
						if member(arg, "-c", "--command", "-e", "-m", "-s", "-i") || strings.HasPrefix(arg, "-c") || strings.HasPrefix(arg, "-e") || strings.HasPrefix(arg, "--command=") {
							return "", fmt.Errorf("interpreted inline payload not approved")
						}
						if index == 0 {
							continue
						}
						if !scriptSeen && strings.HasPrefix(arg, "-") {
							safe := kind == "python" && member(arg, "-u", "-B", "-E", "-I") || kind == "shell" && member(arg, "--noprofile", "--norc")
							if !safe {
								return "", fmt.Errorf("interpreter loader option not approved")
							}
						}
						if !strings.HasPrefix(arg, "-") && !scriptSeen {
							if !filepath.IsAbs(arg) || !pinned[arg] || trustedPath(arg, 0, false) != nil {
								return "", fmt.Errorf("interpreter script must be exact protected pinned absolute file")
							}
							scriptSeen = true
						}
						if strings.HasPrefix(arg, "/") && arg != path {
							if !pinned[arg] || trustedPath(arg, 0, false) != nil {
								return "", fmt.Errorf("interpreter argument must be exact protected pinned file")
							}
						}
					}
					if rootRuntime && !scriptSeen {
						return "", fmt.Errorf("root interpreter entrypoint missing")
					}
				}
			}
		}
	}
	if m["EnvironmentFiles"] != "" {
		pattern := regexp.MustCompile(`(?:^| )(-?/[^ (]+) \(ignore_errors=(?:yes|no)\)`)
		matches := pattern.FindAllStringSubmatch(m["EnvironmentFiles"], -1)
		if len(matches) == 0 || strings.TrimSpace(pattern.ReplaceAllString(m["EnvironmentFiles"], "")) != "" {
			return "", fmt.Errorf("environment-file identity unverifiable")
		}
		for _, match := range matches {
			path := strings.TrimPrefix(match[1], "-")
			if !pinned[path] || trustedPath(path, 0, false) != nil {
				return "", fmt.Errorf("every environment file must be protected and pinned")
			}
			if rootRuntime {
				contents, e := readBoundedFile(path, 128<<10)
				if e != nil || unsafeRootEnvironment(string(contents)) {
					return "", fmt.Errorf("root environment-file loader settings unsafe")
				}
			}
		}
	}
	return digest(m), nil
}

// Observation approval binds metadata of an existing protected unit. It never
// reads or approves its executable, script, environment files or code loaders.
// The distinct hash domain cannot become an effect approval by dropping the flag.
func canonicalSystemdObservation(p Profile, m map[string]string) (string, error) {
	if err := p.validateObservationOnly(); err != nil {
		return "", err
	}
	canonicalSystemdDependencySets(m)
	if m["FragmentPath"] == "" {
		return "", fmt.Errorf("transient/unbound unit cannot be observed")
	}
	for _, path := range append([]string{m["FragmentPath"]}, strings.Fields(m["DropInPaths"])...) {
		if err := trustedPath(path, 0, false); err != nil {
			return "", err
		}
		hash, err := protectedCodeHash(path, 0)
		if err != nil {
			return "", err
		}
		m["definition_file:"+path] = hash
	}
	for _, field := range []string{"ExecCondition", "ExecConditionEx", "ExecStart", "ExecStartPre", "ExecStartPost", "ExecStop", "ExecStopPost", "ExecStartEx", "ExecStartPreEx", "ExecStartPostEx", "ExecStopEx", "ExecStopPostEx"} {
		configured, err := canonicalExec(m[field])
		if err != nil {
			return "", err
		}
		m[field] = configured
	}
	return digest(struct {
		Mode       string
		Properties map[string]string
	}{"observation-only", m}), nil
}

// systemctl show enumerates these dependency sets in unstable order. Freeze
// their membership while preserving every ordered command, environment and
// definition-file sequence in the approval hash.
func canonicalSystemdDependencySets(m map[string]string) {
	for _, property := range []string{"Requires", "PartOf", "BindsTo"} {
		value, present := m[property]
		if !present {
			continue
		}
		members := strings.Fields(value)
		sort.Strings(members)
		unique := members[:0]
		for _, unit := range members {
			if len(unique) == 0 || unique[len(unique)-1] != unit {
				unique = append(unique, unit)
			}
		}
		m[property] = strings.Join(unique, " ")
	}
}

// Root command admission is positive, rather than a growing blacklist of
// interpreters, wrappers and utilities with code hooks. Native model binaries
// with no reviewed argv/code-loading contract remain unavailable for root
// control; their existing runtime is never rewritten by this adapter.
func supportedRootCommand(path string, argv []string) bool {
	if rootHelperPath(path, "true") || rootHelperPath(path, "false") {
		return true
	}
	if !rootHelperPath(path, "sleep") || len(argv) < 2 || len(argv) > 16 {
		return false
	}
	for _, argument := range argv[1:] {
		if !regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)?[smhd]?$`).MatchString(argument) {
			return false
		}
	}
	return true
}

func rootHelperPath(path, helper string) bool {
	for _, source := range []string{"/usr/bin/" + helper, "/bin/" + helper} {
		resolved, err := filepath.EvalSymlinks(source)
		if err == nil && resolved == path && trustedPath(resolved, 0, false) == nil {
			return true
		}
	}
	return false
}

func unsafeRootEnvironment(raw string) bool {
	for _, assignment := range regexp.MustCompile(`(?:^|[\s"'])([A-Za-z_][A-Za-z0-9_]*)\s*=`).FindAllStringSubmatch(raw, -1) {
		key := assignment[1]
		if strings.HasPrefix(key, "LD_") && key != "LD_LIBRARY_PATH" || member(key, "BASH_ENV", "ENV", "SHELLOPTS", "BASHOPTS", "CDPATH", "GCONV_PATH", "LOCPATH", "PYTHONPATH", "PYTHONHOME", "PYTHONSTARTUP", "PYTHONUSERBASE", "PERL5OPT", "PERL5LIB", "PERLLIB", "RUBYOPT", "RUBYLIB", "NODE_OPTIONS", "NODE_PATH", "LUA_PATH", "LUA_CPATH", "LUA_INIT", "JAVA_TOOL_OPTIONS", "JDK_JAVA_OPTIONS", "_JAVA_OPTIONS", "CLASSPATH", "PHPRC", "PHP_INI_SCAN_DIR", "TCL_LIBRARY", "R_LIBS", "R_LIBS_USER", "DOTNET_STARTUP_HOOKS", "DOTNET_ADDITIONAL_DEPS") {
			return true
		}
	}
	for _, m := range regexp.MustCompile(`(?:^|[\s"'])(?:LD_LIBRARY_PATH|PATH|HOME)\s*=([^\s"']*)`).FindAllStringSubmatch(raw, -1) {
		for _, path := range strings.Split(m[1], ":") {
			if trustedPath(path, 0, true) != nil {
				return true
			}
		}
	}
	return false
}

// Version suffixes are still loaders. Unsupported language launchers and ELF
// loaders cannot be made safe by merely pinning their executable inode.
func interpreterKind(base string) string {
	// Generic command, privilege, namespace and service/container wrappers
	// resolve a second executable or code source. They are outside the root
	// adapter's supported argv grammar, even when the wrapper itself is ELF.
	if member(base, "timeout", "nice", "nohup", "setsid", "stdbuf", "flock", "taskset", "ionice", "chrt", "prlimit", "setpriv", "numactl", "runuser", "su", "sudo", "sg", "newgrp", "xargs", "parallel", "watch", "daemon", "start-stop-daemon", "systemd-run", "systemctl", "docker", "podman", "ctr", "nerdctl", "crictl", "runc", "crun", "bwrap", "firejail", "unshare", "nsenter", "chroot", "uv", "uvx", "npm", "npx", "yarn", "corepack", "go", "gcc", "g++", "cc", "clang", "zig", "cargo", "rustc", "mono", "R", "guile", "sbcl", "clisp", "scheme", "ocaml", "runhaskell", "scala", "swift") {
		return "unsupported"
	}
	if member(base, "bash", "sh", "dash", "zsh") {
		return "shell"
	}
	if regexp.MustCompile(`^(?:python|pypy)(?:[0-9]+(?:\.[0-9]+)*[mdut]*)?$`).MatchString(base) {
		return "python"
	}
	for _, prefix := range []string{"python", "pypy", "bash", "dash", "zsh", "csh", "tcsh", "perl", "ruby", "node", "deno", "bun", "lua", "php", "julia", "Rscript", "tclsh", "wish", "awk", "gawk", "mawk", "java", "dotnet", "env", "ksh", "fish", "ash", "busybox", "ld-linux", "ld-musl", "ld.so", "ld64.so", "gcc-", "g++-", "clang-"} {
		if strings.HasPrefix(base, prefix) {
			return "unsupported"
		}
	}
	return ""
}
func unitPrivilegeSyntaxSafe(raw string) bool {
	raw = strings.NewReplacer("\\\r\n", " ", "\\\n", " ").Replace(raw)
	for _, m := range regexp.MustCompile(`(?m)^\s*Exec(?:Condition|Start(?:Pre|Post)?|Stop(?:Post)?)\s*=\s*([-:@+!|]*)`).FindAllStringSubmatch(raw, -1) {
		if strings.ContainsAny(m[1], "+!|") {
			return false
		}
	}
	return true
}
func canonicalExec(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	pattern := regexp.MustCompile(`\{ ([^}]*)\}`)
	entries := pattern.FindAllStringSubmatch(raw, -1)
	if len(entries) == 0 {
		return "", fmt.Errorf("unit execution definition unverifiable")
	}
	if strings.TrimSpace(pattern.ReplaceAllString(raw, "")) != "" {
		return "", fmt.Errorf("unit execution contains unparsed configuration")
	}
	out := []string{}
	for _, entry := range entries {
		parts := strings.Split(entry[1], " ; ")
		keep := []string{}
		seen := map[string]bool{}
		for _, part := range parts {
			part = strings.TrimSpace(part)
			key, value, ok := strings.Cut(part, "=")
			if !ok || seen[key] {
				return "", fmt.Errorf("unit execution fields ambiguous")
			}
			seen[key] = true
			switch key {
			case "path":
				if !filepath.IsAbs(value) || filepath.Clean(value) != value || strings.ContainsAny(value, " \t\r\n") {
					return "", fmt.Errorf("unit executable identity missing")
				}
			case "argv[]":
				if strings.TrimSpace(value) == "" {
					return "", fmt.Errorf("unit argv missing")
				}
			case "ignore_errors":
				if !member(value, "yes", "no") {
					return "", fmt.Errorf("unit error policy unknown")
				}
			case "flags":
				for _, flag := range strings.Fields(value) {
					if !member(flag, "none", "ignore-failure", "no-env-expand") {
						return "", fmt.Errorf("unit effective execution flag not supported")
					}
				}
			case "start_time", "stop_time", "pid", "code", "status":
			default:
				return "", fmt.Errorf("unit execution field unknown")
			}
			if strings.HasPrefix(part, "path=") || strings.HasPrefix(part, "argv[]=") || strings.HasPrefix(part, "ignore_errors=") || strings.HasPrefix(part, "flags=") {
				keep = append(keep, part)
			}
		}
		if !seen["path"] || !seen["argv[]"] {
			return "", fmt.Errorf("unit execution configuration missing")
		}
		out = append(out, "{ "+strings.Join(keep, " ; ")+" }")
	}
	return strings.Join(out, " "), nil
}
func systemdInspect(ctx context.Context, p Profile) (Instance, error) {
	m, e := systemdShow(ctx, p, "LoadState,ActiveState,SubState,InvocationID,MainPID,ControlPID,Job,ControlGroup,ExecMainStartTimestampMonotonic")
	if e != nil {
		return Instance{}, e
	}
	boot, e := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if e != nil || strings.TrimSpace(string(boot)) == "" {
		return Instance{}, fmt.Errorf("unit boot identity unavailable")
	}
	return systemdInstance(p, m, string(boot), func(group string) bool {
		return systemdCgroupEmpty("/sys/fs/cgroup", group)
	})
}

// A terminal ActiveState alone does not prove shutdown: an ExecStartPre/
// ExecStop process, queued activation, or remaining cgroup task may still live.
// Missing properties are unavailable evidence, never equivalent to zero/empty.
func systemdInstance(p Profile, m map[string]string, boot string, cgroupEmpty func(string) bool) (Instance, error) {
	for _, key := range []string{"LoadState", "ActiveState", "SubState", "InvocationID", "MainPID", "ControlPID", "Job", "ControlGroup", "ExecMainStartTimestampMonotonic"} {
		if _, ok := m[key]; !ok {
			return Instance{}, fmt.Errorf("unit runtime property unavailable: %s", key)
		}
	}
	boot = strings.TrimSpace(boot)
	if m["LoadState"] != "loaded" || m["SubState"] == "" || !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`).MatchString(boot) {
		return Instance{}, fmt.Errorf("unit runtime observation unavailable")
	}
	invocation := m["InvocationID"]
	if invocation != "" && (len(invocation) != 32 || strings.Trim(invocation, "0123456789abcdef") != "") || member(m["ActiveState"], "failed", "active", "activating", "deactivating") && invocation == "" {
		return Instance{}, fmt.Errorf("unit runtime invocation identity unavailable")
	}
	timestamp := m["ExecMainStartTimestampMonotonic"]
	if timestamp == "" || strings.Trim(timestamp, "0123456789") != "" || (len(timestamp) > 1 && timestamp[0] == '0') {
		return Instance{}, fmt.Errorf("unit runtime timestamp ambiguous")
	}
	if _, err := strconv.ParseUint(timestamp, 10, 64); err != nil {
		return Instance{}, fmt.Errorf("unit runtime timestamp ambiguous")
	}
	parsePID := func(raw string) (int, error) {
		if raw == "" || strings.Trim(raw, "0123456789") != "" || (len(raw) > 1 && raw[0] == '0') {
			return 0, fmt.Errorf("unit runtime PID ambiguous")
		}
		pid, err := strconv.ParseUint(raw, 10, 31)
		return int(pid), err
	}
	mainPID, e := parsePID(m["MainPID"])
	if e != nil {
		return Instance{}, e
	}
	controlPID, e := parsePID(m["ControlPID"])
	if e != nil {
		return Instance{}, e
	}
	state := "unknown"
	switch m["ActiveState"] {
	case "active":
		state = "running"
	case "activating":
		state = "starting"
	case "deactivating":
		state = "stopping"
	case "inactive":
		state = "stopped"
	case "failed":
		state = "failed"
	}
	terminal := state == "stopped" && m["SubState"] == "dead" || state == "failed" && m["SubState"] == "failed"
	shutdown := terminal && mainPID == 0 && controlPID == 0 && m["Job"] == "" && (m["ControlGroup"] == "" || cgroupEmpty != nil && cgroupEmpty(m["ControlGroup"]))
	identity := digest([]string{boot, p.Target.Unit, m["ActiveState"], m["SubState"], m["InvocationID"], m["ExecMainStartTimestampMonotonic"], m["ControlGroup"]})
	if state == "stopped" && shutdown {
		identity = "stopped"
	}
	if state == "running" || state == "starting" || state == "stopping" {
		if m["InvocationID"] == "" || m["ControlGroup"] == "" {
			return Instance{}, fmt.Errorf("unit runtime identity unavailable")
		}
		identity = digest([]string{boot, p.Target.Unit, m["InvocationID"], m["ExecMainStartTimestampMonotonic"], m["ControlGroup"]})
	}
	return Instance{State: state, Identity: identity, PID: mainPID, Available: true, ShutdownVerified: shutdown}, nil
}

// cgroup.events' populated flag covers the whole subtree on cgroup v2. Verify
// both it and every cgroup.procs file, and reread the flag after traversal. A
// retained but missing/unreadable/unsupported cgroup cannot prove shutdown.
func systemdCgroupEmpty(root, group string) bool {
	if group == "" || group == "/" || !strings.HasPrefix(group, "/") || filepath.Clean(group) != group {
		return false
	}
	path := root
	for _, component := range strings.Split(strings.TrimPrefix(group, "/"), "/") {
		path = filepath.Join(path, component)
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return false
		}
	}
	unpopulated := func() bool {
		b, err := readBoundedFile(filepath.Join(path, "cgroup.events"), 4096)
		if err != nil {
			return false
		}
		seen := false
		for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
			parts := strings.Fields(line)
			if len(parts) != 2 {
				return false
			}
			if parts[0] == "populated" {
				if seen || parts[1] != "0" {
					return false
				}
				seen = true
			}
		}
		return seen
	}
	if !unpopulated() {
		return false
	}
	directories := 0
	err := filepath.WalkDir(path, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("cgroup observation unavailable")
		}
		if entry.IsDir() {
			directories++
			if directories > 256 {
				return fmt.Errorf("cgroup observation budget exceeded")
			}
			b, err := readBoundedFile(filepath.Join(current, "cgroup.procs"), 4096)
			if err != nil || len(b) > 0 && strings.TrimSpace(string(b)) != "" {
				return fmt.Errorf("cgroup tasks present or unavailable")
			}
		}
		return nil
	})
	return err == nil && unpopulated()
}
