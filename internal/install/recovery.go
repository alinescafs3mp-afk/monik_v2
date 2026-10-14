package install

import (
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/alinescafs3mp-afk/monik_v2/internal/agent/configfile"
	"github.com/alinescafs3mp-afk/monik_v2/internal/idgen"
	"github.com/alinescafs3mp-afk/monik_v2/internal/jsonutil"
	"github.com/alinescafs3mp-afk/monik_v2/internal/secure"
)

type recoveryPoint struct {
	ID              string            `json:"id"`
	AgentID         string            `json:"agent_id"`
	ControllerID    string            `json:"controller_id"`
	StateDir        string            `json:"state_dir"`
	Flavor          string            `json:"flavor"`
	RuntimeMode     string            `json:"runtime_mode"`
	Managed         bool              `json:"managed"`
	Files           map[string]string `json:"files"`
	CandidateWorker string            `json:"candidate_worker"`
	CandidateHost   string            `json:"candidate_host"`
	CandidateUnit   string            `json:"candidate_unit"`
	CandidateFlavor string            `json:"candidate_flavor"`
	Settled         bool              `json:"settled"`
}

// Recovery is outside collector-writable state. No credential, applied config,
// endpoint generation, spool, or model file is rewound during local rollback.
func saveRecovery(opts Options, old configfile.File, newWorker, newHost []byte, plannedUnit string, spark bool) error {
	flavorNext := "general"
	if spark {
		flavorNext = "spark-light"
	}
	if !opts.SkipSystemctl {
		for _, p := range []string{filepath.Join(opts.Prefix, "recovery.json"), filepath.Join(opts.Prefix, "recovery")} {
			if e := checkRecoveryPath(p); e != nil {
				return e
			}
		}
	}
	priorBytes, e := readInstallSource(filepath.Join(opts.Prefix, "recovery.json"))
	if e == nil {
		var prior recoveryPoint
		if e = jsonutil.Unmarshal(priorBytes, &prior); e != nil {
			return fmt.Errorf("prior recovery point malformed; preserve it")
		}
		if !prior.Settled {
			if prior.AgentID != old.AgentID || prior.ControllerID != old.ControllerID || prior.StateDir != opts.StateDir {
				return fmt.Errorf("unfinished recovery belongs to another binding")
			}
			if prior.CandidateWorker != secure.SHA256Bytes(newWorker) || prior.CandidateHost != secure.SHA256Bytes(newHost) || prior.CandidateUnit != secure.SHA256Bytes([]byte(plannedUnit)) || prior.CandidateFlavor != flavorNext {
				return fmt.Errorf("unfinished local installation: retry exact candidate or rollback before replacement")
			}
			if e = verifyRecoveryParts(opts, prior); e != nil {
				return e
			}
			// Do not replace the previous-good snapshot with partially published bytes.
			return nil
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	workerPath := filepath.Join(opts.StateDir, "bin", "monik-agent")
	worker, err := readInstallSource(workerPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	flavor := old.AgentFlavor
	if flavor == "" {
		flavor = "general"
	}
	next := "general"
	if spark {
		next = "spark-light"
	}
	sameWorker := secure.SHA256Bytes(worker) == secure.SHA256Bytes(newWorker) && flavor == next
	if !old.Managed && !sameWorker {
		return fmt.Errorf("different orphan worker preserved; inspect partial installation before replacement")
	}
	same := sameWorker
	if spark && old.Managed {
		same = same && old.RuntimeMode == "direct"
	}
	unit, e := readInstallSource(opts.UnitPath)
	same = same && (os.IsNotExist(e) || e == nil && string(unit) == plannedUnit)
	if !spark {
		host, e := readInstallSource(filepath.Join(opts.Prefix, "monik-service-host"))
		same = same && e == nil && secure.SHA256Bytes(host) == secure.SHA256Bytes(newHost)
	}
	if same {
		return nil
	}
	if !opts.SkipSystemctl {
		if err = validateLinuxBinary(worker, runtime.GOARCH); err != nil {
			return err
		}
	}
	r := recoveryPoint{ID: idgen.New(), AgentID: old.AgentID, ControllerID: old.ControllerID, StateDir: opts.StateDir, Flavor: flavor, RuntimeMode: old.RuntimeMode, Managed: old.Managed, Files: map[string]string{}, CandidateWorker: secure.SHA256Bytes(newWorker), CandidateHost: secure.SHA256Bytes(newHost), CandidateUnit: secure.SHA256Bytes([]byte(plannedUnit)), CandidateFlavor: flavorNext}
	if !opts.SkipSystemctl {
		for _, path := range []string{filepath.Join(opts.Prefix, "recovery"), filepath.Join(opts.Prefix, "recovery.json")} {
			if e := checkRecoveryPath(path); e != nil {
				return e
			}
		}
	}
	root := filepath.Join(opts.Prefix, "recovery", r.ID)
	if err = os.MkdirAll(root, 0700); err != nil {
		return err
	}
	parts := map[string]string{"worker": workerPath, "unit": opts.UnitPath, "prefix-worker": filepath.Join(opts.Prefix, "monik-agent")}
	if flavor == "general" {
		parts["host"] = filepath.Join(opts.Prefix, "monik-service-host")
	}
	for name, path := range parts {
		b, e := readInstallSource(path)
		if os.IsNotExist(e) && name == "prefix-worker" {
			continue
		}
		if e != nil {
			return fmt.Errorf("backup %s: %w", name, e)
		}
		if e = secure.AtomicWrite(filepath.Join(root, name), b, 0600); e != nil {
			return e
		}
		r.Files[name] = secure.SHA256Bytes(b)
	}
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if !opts.SkipSystemctl {
		parent, e := os.Open(filepath.Dir(root))
		if e != nil {
			return e
		}
		e = parent.Sync()
		_ = parent.Close()
		if e != nil {
			return e
		}
	}
	return secure.AtomicWrite(filepath.Join(opts.Prefix, "recovery.json"), b, 0600)
}

func verifyRecoveryParts(opts Options, r recoveryPoint) error {
	if len(r.ID) != 36 || strings.ContainsAny(r.ID, "/\\.\r\n") || (r.Flavor != "general" && r.Flavor != "spark-light") || len(r.Files["worker"]) != 64 || len(r.Files["unit"]) != 64 || r.Flavor == "general" && len(r.Files["host"]) != 64 {
		return fmt.Errorf("incomplete previous-good recovery point")
	}
	root := filepath.Join(opts.Prefix, "recovery", r.ID)
	if !opts.SkipSystemctl {
		if e := checkRecoveryPath(root); e != nil {
			return e
		}
	}
	for name, sum := range r.Files {
		if !strings.Contains("|worker|unit|host|prefix-worker|", "|"+name+"|") || len(sum) != 64 {
			return fmt.Errorf("invalid recovery part")
		}
		p := filepath.Join(root, name)
		if !opts.SkipSystemctl {
			if e := checkRecoveryPath(p); e != nil {
				return e
			}
		}
		b, e := readInstallSource(p)
		if e != nil {
			return e
		}
		if secure.SHA256Bytes(b) != sum {
			return fmt.Errorf("previous-good recovery part %s corrupted", name)
		}
	}
	return nil
}

func RollbackLinux(opts Options) error {
	if opts.User == "" {
		opts.User = "monik"
	}
	if runtime.GOOS != "linux" || (!opts.SkipSystemctl && os.Geteuid() != 0) {
		return fmt.Errorf("local Linux root authority required")
	}
	if !filepath.IsAbs(opts.Prefix) || !filepath.IsAbs(opts.StateDir) || !filepath.IsAbs(opts.ConfigPath) || !filepath.IsAbs(opts.UnitPath) || unsafeInstallDir(opts.Prefix) || unsafeInstallDir(opts.StateDir) || withinDir(opts.Prefix, opts.StateDir) || withinDir(opts.StateDir, opts.Prefix) {
		return fmt.Errorf("invalid rollback paths")
	}
	if !opts.SkipSystemctl {
		unlock, e := lockNativeInstall()
		if e != nil {
			return e
		}
		defer unlock()
		if e = checkRecoveryPath(filepath.Join(opts.Prefix, "recovery.json")); e != nil {
			return e
		}
		for _, p := range []string{opts.Prefix, opts.UnitPath} {
			if e = checkRootAncestors(p, false); e != nil {
				return e
			}
		}
		if e = checkRootAncestors(opts.StateDir, true); e != nil {
			return e
		}
		if filepath.Base(opts.UnitPath) != "monik-agent.service" {
			return fmt.Errorf("unexpected unit")
		}
	}
	b, err := readInstallSource(filepath.Join(opts.Prefix, "recovery.json"))
	if err != nil {
		return err
	}
	var r recoveryPoint
	if err = jsonutil.Unmarshal(b, &r); err != nil {
		return err
	}
	if len(r.ID) != 36 || strings.ContainsAny(r.ID, "/\\.\r\n") || (r.Flavor != "general" && r.Flavor != "spark-light") || filepath.Clean(r.StateDir) != filepath.Clean(opts.StateDir) || !r.Managed {
		return fmt.Errorf("invalid recovery point")
	}
	if err = verifyRecoveryParts(opts, r); err != nil {
		return err
	}
	root := filepath.Join(opts.Prefix, "recovery", r.ID)
	if !opts.SkipSystemctl {
		if err = checkRecoveryPath(root); err != nil {
			return err
		}
	}
	files := map[string][]byte{}
	for name, sum := range r.Files {
		if name != "worker" && name != "unit" && name != "host" && name != "prefix-worker" {
			return fmt.Errorf("unsupported recovery part")
		}
		b, e := readInstallSource(filepath.Join(root, name))
		if e != nil {
			return e
		}
		if secure.SHA256Bytes(b) != sum {
			return fmt.Errorf("recovery %s digest mismatch", name)
		}
		files[name] = b
	}
	if len(files["worker"]) == 0 || len(files["unit"]) == 0 || (r.Flavor == "general" && len(files["host"]) == 0) {
		return fmt.Errorf("incomplete recovery point")
	}
	if !opts.SkipSystemctl {
		if err = validateLinuxBinary(files["worker"], runtime.GOARCH); err != nil {
			return err
		}
		if r.Flavor == "general" {
			if err = validateLinuxBinary(files["host"], runtime.GOARCH); err != nil {
				return err
			}
		}
	}
	if !opts.SkipSystemctl {
		if err = inspectPrivateState(opts.StateDir); err != nil {
			return err
		}
	}
	current, err := configfile.Load(opts.ConfigPath)
	if err != nil {
		return err
	}
	validIdentity := func(f configfile.File) bool {
		return f.AgentID == r.AgentID && f.ControllerID == r.ControllerID && filepath.Clean(f.StateDir) == filepath.Clean(opts.StateDir) && filepath.Dir(filepath.Clean(opts.ConfigPath)) == filepath.Clean(opts.StateDir) && withinDir(opts.StateDir, f.CredentialPath)
	}
	if !validIdentity(current.File) {
		return fmt.Errorf("recovery belongs to a different enrollment; nothing changed")
	}
	var ctl string
	if !opts.SkipSystemctl {
		ctl, err = systemTool("systemctl")
		if err != nil {
			return err
		}
		if err = runSystemTool(ctl, "stop", "monik-agent.service"); err != nil {
			return err
		}
		latest, e := readPrivateConfig(opts.StateDir, filepath.Base(opts.ConfigPath))
		if e != nil {
			return e
		}
		if !validIdentity(latest) {
			return fmt.Errorf("enrollment changed during rollback")
		}
		current.File = latest
	}
	if err = writePrivateState(opts.StateDir, "bin/monik-agent", files["worker"], 0755); err != nil {
		return err
	}
	// The service-owned active slot may differ from the protected CLI after a
	// signed update. Never promote those writable bytes into a root CLI slot.
	if prefixWorker := files["prefix-worker"]; len(prefixWorker) > 0 {
		if err = secure.AtomicWrite(filepath.Join(opts.Prefix, "monik-agent"), prefixWorker, 0755); err != nil {
			return err
		}
	}
	if len(files["host"]) > 0 {
		if err = secure.AtomicWrite(filepath.Join(opts.Prefix, "monik-service-host"), files["host"], 0755); err != nil {
			return err
		}
	}
	current.File.AgentFlavor = r.Flavor
	if r.Flavor == "general" {
		current.File.AgentFlavor = ""
	}
	current.File.RuntimeMode = r.RuntimeMode
	current.File.Managed = r.Managed
	b, err = json.MarshalIndent(current.File, "", "  ")
	if err != nil {
		return err
	}
	if err = writePrivateState(opts.StateDir, filepath.Base(opts.ConfigPath), b, 0600); err != nil {
		return err
	}
	if err = secure.AtomicWrite(opts.UnitPath, files["unit"], 0644); err != nil {
		return err
	}
	if !opts.SkipSystemctl {
		u, e := ensureServiceUser(opts.User)
		if e != nil {
			return e
		}
		uid, gid, e := serviceIDs(u)
		if e != nil {
			return e
		}
		if err = ownPrivateState(opts.StateDir, uid, gid); err != nil {
			return err
		}
		for _, args := range [][]string{{"daemon-reload"}, {"enable", "monik-agent.service"}, {"start", "monik-agent.service"}, {"is-active", "--quiet", "monik-agent.service"}} {
			if err = runSystemTool(ctl, args...); err != nil {
				return fmt.Errorf("rollback bytes restored but service readiness NOT confirmed: %w", err)
			}
		}
	}
	return settleRecovery(opts, r.CandidateWorker, r.CandidateHost, r.CandidateUnit, r.CandidateFlavor)
}

func settleRecovery(opts Options, worker, host, unit, flavor string) error {
	path := filepath.Join(opts.Prefix, "recovery.json")
	b, e := readInstallSource(path)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	var r recoveryPoint
	if e = jsonutil.Unmarshal(b, &r); e != nil {
		return e
	}
	if r.CandidateWorker != worker || r.CandidateHost != host || r.CandidateUnit != unit || r.CandidateFlavor != flavor {
		return nil
	}
	r.Settled = true
	b, e = json.Marshal(r)
	if e != nil {
		return e
	}
	return secure.AtomicWrite(path, b, 0600)
}

func serviceIDs(u *user.User) (int, int, error) {
	uid, e := strconv.Atoi(u.Uid)
	if e != nil {
		return 0, 0, e
	}
	gid, e := strconv.Atoi(u.Gid)
	if e != nil {
		return 0, 0, e
	}
	if uid <= 0 || gid <= 0 {
		return 0, 0, fmt.Errorf("unprivileged service identity required")
	}
	return uid, gid, nil
}
