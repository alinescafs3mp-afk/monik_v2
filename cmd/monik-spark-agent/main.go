//go:build linux

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/agent/configfile"
	"github.com/alinescafs3mp-afk/monik_v2/internal/agent/setup"
	"github.com/alinescafs3mp-afk/monik_v2/internal/idgen"
	"github.com/alinescafs3mp-afk/monik_v2/internal/install"
	"github.com/alinescafs3mp-afk/monik_v2/internal/spark"
	"github.com/alinescafs3mp-afk/monik_v2/internal/version"
)

const defaultBroker = "/run/monik-spark/control.sock"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(args []string) error {
	if len(args) == 0 {
		usage()
		return nil
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	switch args[0] {
	case "version", "--version", "-version":
		fmt.Printf("monik-spark-agent %s (%s) %s/%s flavor=spark-light\n", version.Version, version.Commit, runtime.GOOS, runtime.GOARCH)
		return nil
	case "run":
		fs := flag.NewFlagSet("run", flag.ContinueOnError)
		cfg := fs.String("config", setup.DefaultConfigPath(), "existing enrolled config")
		broker := fs.String("broker", defaultBroker, "locally approved typed Unix broker")
		if e := fs.Parse(args[1:]); e != nil {
			return e
		}
		if e := setup.WaitForApproval(ctx, *cfg, os.Stdout); e != nil {
			return e
		}
		w, e := spark.OpenWorker(*cfg, *broker)
		if e != nil {
			return e
		}
		return w.Run(ctx)
	case "setup":
		return setupCommand(args[1:])
	case "service":
		return serviceCommand(args[1:])
	case "broker":
		fs := flag.NewFlagSet("broker", flag.ContinueOnError)
		policy := fs.String("policy", "/etc/monik-spark/policy.json", "locally approved root-owned policy")
		socket := fs.String("socket", defaultBroker, "protected Unix socket")
		if e := fs.Parse(args[1:]); e != nil {
			return e
		}
		return spark.ServeBroker(ctx, *policy, *socket)
	case "approve":
		fs := flag.NewFlagSet("approve", flag.ContinueOnError)
		draft := fs.String("draft", "", "LOCAL reviewed typed draft")
		output := fs.String("output", "/etc/monik-spark/policy.json", "root-owned policy destination")
		enable := fs.Bool("enable", false, "explicitly enable reviewed exact actions/log sources")
		if e := fs.Parse(args[1:]); e != nil {
			return e
		}
		if *draft == "" {
			return fmt.Errorf("--draft required")
		}
		if e := spark.ApprovePolicy(ctx, *draft, *output, *enable); e != nil {
			return e
		}
		fmt.Println("effective local binding frozen; enabled=", *enable)
		return nil
	case "verify-policy":
		fs := flag.NewFlagSet("verify-policy", flag.ContinueOnError)
		policy := fs.String("policy", "/etc/monik-spark/policy.json", "approved local policy")
		if e := fs.Parse(args[1:]); e != nil {
			return e
		}
		p, e := spark.LoadPolicy(*policy, 0)
		if e != nil {
			return e
		}
		fmt.Printf("policy valid; enabled=%v profiles=%d (no effects)\n", p.Enabled, len(p.Profiles))
		return nil
	case "status":
		fs := flag.NewFlagSet("status", flag.ContinueOnError)
		socket := fs.String("socket", defaultBroker, "local broker")
		if e := fs.Parse(args[1:]); e != nil {
			return e
		}
		c, end := context.WithTimeout(ctx, 10*time.Second)
		defer end()
		snapshot, e := (spark.BrokerClient{Socket: *socket}).Snapshot(c)
		if e != nil {
			return e
		}
		return json.NewEncoder(os.Stdout).Encode(snapshot)
	case "action":
		fs := flag.NewFlagSet("action", flag.ContinueOnError)
		socket := fs.String("socket", defaultBroker, "same coordinator used by web/CLI/TUI")
		profile := fs.String("profile", "", "approved profile ID")
		action := fs.String("action", "", "start|stop|restart")
		operation := fs.String("operation", idgen.New(), "stable operation ID for dedup")
		confirm := fs.Bool("confirm-disruption", false, "use existing approved stop policy")
		if e := fs.Parse(args[1:]); e != nil {
			return e
		}
		receipt, e := spark.LocalAction(ctx, spark.BrokerClient{Socket: *socket}, *profile, *action, *operation, *confirm)
		if e != nil {
			return e
		}
		if e = json.NewEncoder(os.Stdout).Encode(receipt); e != nil {
			return e
		}
		if receipt.Status == "rejected" || receipt.Status == "failed" || receipt.Status == "unknown_result" {
			return fmt.Errorf("action did not confirm success: %s", receipt.ErrorCode)
		}
		return nil
	case "collect":
		c := spark.NewCollector()
		host, caps, extension := c.Snapshot(time.Now().UTC())
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"host": host, "capabilities": caps, "spark": extension, "scope": "read_only_local_sample"})
	case "discover":
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"candidates": spark.DiscoverProcesses(), "control_allowed": false, "scope": "read_only_candidates_no_launch"})
	case "doctor":
		fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
		cfg := fs.String("config", setup.DefaultConfigPath(), "config")
		if e := fs.Parse(args[1:]); e != nil {
			return e
		}
		s, e := configfile.Load(*cfg)
		if e != nil {
			return e
		}
		fmt.Printf("identity=%s controller=%s flavor=spark-light; run non-root outside Docker group; no remote update/restart\n", s.File.AgentID, s.File.ControllerURL)
		return nil
	case "help", "--help", "-h":
		usage()
		return nil
	default:
		usage()
		return fmt.Errorf("unknown command")
	}
}
func usage() {
	fmt.Printf(`monik-spark-agent %s (Linux native, spark-light)
  setup --non-interactive --profile PATH
  run --config PATH [--broker %s]
  service plan|install|convert|rollback|status|uninstall --config PATH
  collect | discover | doctor --config PATH
  approve --draft PATH --output PATH [--enable]  (local root only)
  verify-policy --policy PATH
  broker --policy PATH --socket PATH          (root typed Unix service)
  status --socket PATH
  action --profile ID --action start|stop|restart [--confirm-disruption]
         [--operation STABLE_ID] [--socket PATH]
Remote restart is staged and requires fresh controller authorization after stop.
Local restart obtains a fresh bound linked start after the verified stop.
`, version.Version, defaultBroker)
}
func setupCommand(args []string) error {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	profile := fs.String("profile", "", "trusted enrollment profile")
	noninteractive := fs.Bool("non-interactive", false, "require profile")
	if e := fs.Parse(args); e != nil {
		return e
	}
	var p *setup.Profile
	var e error
	if *profile != "" {
		p, e = setup.LoadProfile(*profile)
	} else if *noninteractive {
		return fmt.Errorf("--profile required")
	} else {
		p, e = setup.Interactive(os.Stdin, os.Stdout)
	}
	if e != nil {
		return e
	}
	state, e := setup.Enroll(p)
	if e != nil {
		return e
	}
	state.File.AgentFlavor = "spark-light"
	state.File.RuntimeMode = "direct"
	if e = state.Save(); e != nil {
		return e
	}
	fmt.Printf("configured existing identity=%s config=%s pending=%v\n", state.File.AgentID, filepath.Join(state.File.StateDir, "agent.json"), state.File.PendingRegistration)
	return nil
}
func serviceCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("service plan|install|convert|rollback|status|uninstall")
	}
	fs := flag.NewFlagSet("service", flag.ContinueOnError)
	cfg := fs.String("config", setup.DefaultConfigPath(), "existing enrolled config")
	if e := fs.Parse(args[1:]); e != nil {
		return e
	}
	self, e := os.Executable()
	if e != nil {
		return e
	}
	switch args[0] {
	case "plan":
		state, e := configfile.Load(*cfg)
		if e != nil {
			return e
		}
		fmt.Print(install.PlanSparkUnit(self, *cfg, state.File.StateDir, "monik"))
		return nil
	case "status":
		state, e := configfile.Load(*cfg)
		if e != nil {
			return fmt.Errorf("read installed monitor enrollment: %w", e)
		}
		if !state.File.Managed || state.File.AgentFlavor != "spark-light" || state.File.RuntimeMode != "direct" {
			return fmt.Errorf("selected enrollment does not identify an installed spark-light direct monitor")
		}
		status, observationErr := spark.MonitorServiceStatus(context.Background(), *cfg, state.File.StateDir)
		if e = json.NewEncoder(os.Stdout).Encode(status); e != nil {
			return e
		}
		return observationErr
	case "uninstall":
		if os.Geteuid() != 0 {
			return fmt.Errorf("local root required")
		}
		return install.UninstallLinux()
	case "install", "convert":
		state, e := configfile.Load(*cfg)
		if e != nil {
			return e
		}
		opts := install.LinuxDefaults()
		opts.StateDir = state.File.StateDir
		opts.ConfigPath = *cfg
		opts.WorkerSrc = self
		opts.ConvertFlavor = args[0] == "convert"
		return install.InstallSparkLinux(opts)
	case "rollback":
		state, e := configfile.Load(*cfg)
		if e != nil {
			return e
		}
		opts := install.LinuxDefaults()
		opts.StateDir = state.File.StateDir
		opts.ConfigPath = *cfg
		return install.RollbackLinux(opts)
	default:
		return fmt.Errorf("unknown service command")
	}
}
