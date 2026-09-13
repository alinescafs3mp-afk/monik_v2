package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	goruntime "runtime"
	"syscall"

	agruntime "github.com/alinescafs3mp-afk/monik_v2/internal/agent/runtime"
	"github.com/alinescafs3mp-afk/monik_v2/internal/agent/setup"
	"github.com/alinescafs3mp-afk/monik_v2/internal/servicehost"
	"github.com/alinescafs3mp-afk/monik_v2/internal/version"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "setup":
		os.Exit(cmdSetup(os.Args[2:]))
	case "run":
		os.Exit(cmdRun(os.Args[2:]))
	case "service":
		os.Exit(cmdService(os.Args[2:]))
	case "doctor":
		os.Exit(cmdDoctor(os.Args[2:]))
	case "identity":
		os.Exit(cmdIdentity(os.Args[2:]))
	case "controller":
		os.Exit(cmdController(os.Args[2:]))
	case "version", "-version", "--version":
		fmt.Printf("monik-agent %s (%s) %s/%s\n", version.Version, version.Commit, goruntime.GOOS, goruntime.GOARCH)
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `monik-agent %s
  setup                 enroll with controller
  run --config PATH     run collector/control worker
  service plan|install|status|uninstall
  doctor
  identity reset
  controller show|recover
`, version.Version)
}

func cmdSetup(args []string) int {
	fs := flag.NewFlagSet("setup", flag.ExitOnError)
	profile := fs.String("profile", "", "trusted enrollment profile (yaml/json)")
	url := fs.String("url", "", "controller URL (default bootstrap if unset)")
	code := fs.String("code", "", "enrollment code")
	name := fs.String("name", "", "display name")
	nonint := fs.Bool("non-interactive", false, "fail if required values missing")
	_ = fs.Parse(args)
	var p *setup.Profile
	var err error
	if *profile != "" {
		p, err = setup.LoadProfile(*profile)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	} else if *nonint || *code != "" {
		p = &setup.Profile{ControllerURL: *url, EnrollmentCode: *code, DisplayName: *name}
	} else {
		p, err = setup.Interactive(os.Stdin, os.Stdout)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}
	st, err := setup.Enroll(p)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("enrolled agent_id=%s controller=%s config=%s\n", st.File.AgentID, st.File.ControllerURL, filepath.Join(st.File.StateDir, "agent.json"))
	fmt.Println("foreground mode is unmanaged: remote update/restart unavailable until `monik-agent service install`")
	return 0
}

func cmdRun(args []string) int {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	cfg := fs.String("config", setup.DefaultConfigPath(), "absolute config path")
	_ = fs.Parse(args)
	ag, err := agruntime.Open(*cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	fmt.Printf("monik-agent %s session=%s controller=%s\n", version.Version, ag.Session, ag.State.File.ControllerURL)
	if err := ag.Run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func cmdService(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "service plan|install|status|uninstall")
		return 2
	}
	fs := flag.NewFlagSet("service", flag.ExitOnError)
	cfg := fs.String("config", setup.DefaultConfigPath(), "config path")
	_ = fs.Parse(args[1:])
	self, _ := os.Executable()
	if goruntime.GOOS == "windows" {
		return cmdServiceWindows(args[0], self, *cfg)
	}
	switch args[0] {
	case "plan":
		fmt.Print(servicehost.PlanUnit(filepath.Join(filepath.Dir(self), "monik-service-host"), *cfg, "monik"))
		fmt.Println("# install with existing authority, or run the printed elevated command")
		fmt.Printf("sudo %s service install --config %q\n", self, *cfg)
		return 0
	case "install":
		if os.Geteuid() != 0 {
			fmt.Fprintf(os.Stderr, "installation requires elevation. exact command:\nsudo %s service install --config %q\n", self, *cfg)
			return 1
		}
		unit := "/etc/systemd/system/monik-agent.service"
		hostBin := filepath.Join(filepath.Dir(self), "monik-service-host")
		body := servicehost.PlanUnit(hostBin, *cfg, "monik")
		if err := os.WriteFile(unit, []byte(body), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		st, err := agruntime.Open(*cfg)
		if err == nil {
			st.State.File.Managed = true
			_ = st.State.Save()
		}
		fmt.Println("wrote", unit, "; run: systemctl daemon-reload && systemctl enable --now monik-agent.service")
		return 0
	case "status":
		fmt.Println("run: systemctl status monik-agent.service")
		return 0
	case "uninstall":
		if os.Geteuid() != 0 {
			fmt.Fprintln(os.Stderr, "uninstall requires elevation")
			return 1
		}
		_ = os.Remove("/etc/systemd/system/monik-agent.service")
		fmt.Println("unit removed; credentials and data preserved")
		return 0
	default:
		return 2
	}
}

func cmdServiceWindows(verb, self, cfg string) int {
	hostBin := filepath.Join(filepath.Dir(self), "monik-service-host.exe")
	switch verb {
	case "plan":
		fmt.Print(servicehost.PlanWindowsService(hostBin, self, cfg))
		return 0
	case "install":
		fmt.Println("run elevated:")
		fmt.Print(servicehost.PlanWindowsService(hostBin, self, cfg))
		return 0
	case "status":
		fmt.Println(`sc.exe query MonikAgent`)
		return 0
	case "uninstall":
		fmt.Println(`sc.exe stop MonikAgent & sc.exe delete MonikAgent`)
		return 0
	default:
		return 2
	}
}

func cmdDoctor(args []string) int {
	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
	cfg := fs.String("config", setup.DefaultConfigPath(), "config")
	_ = fs.Parse(args)
	ag, err := agruntime.Open(*cfg)
	if err != nil {
		fmt.Println("config:", err)
		return 1
	}
	fmt.Printf("agent_id=%s controller=%s managed=%v version=%s\n", ag.State.File.AgentID, ag.State.File.ControllerURL, ag.State.File.Managed, version.Version)
	return 0
}

func cmdIdentity(args []string) int {
	if len(args) < 1 || args[0] != "reset" {
		fmt.Fprintln(os.Stderr, "identity reset")
		return 2
	}
	fmt.Println("delete state dir and re-run setup; cloning a pre-enrolled image requires this")
	return 0
}

func cmdController(args []string) int {
	fs := flag.NewFlagSet("controller", flag.ExitOnError)
	cfg := fs.String("config", setup.DefaultConfigPath(), "config")
	profile := fs.String("profile", "", "trusted recovery profile")
	_ = fs.Parse(args[1:])
	if len(args) < 1 {
		return 2
	}
	switch args[0] {
	case "show":
		ag, err := agruntime.Open(*cfg)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Printf("url=%s controller_id=%s generation=%d\n", ag.State.File.ControllerURL, ag.State.File.ControllerID, ag.State.File.EndpointGeneration)
		return 0
	case "recover":
		if *profile == "" {
			fmt.Fprintln(os.Stderr, "--profile required")
			return 2
		}
		fmt.Println("recover: import trusted profile and rewrite controller URL without deleting agent_id")
		return 0
	default:
		return 2
	}
}
