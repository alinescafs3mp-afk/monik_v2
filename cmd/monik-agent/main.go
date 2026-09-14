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

	"github.com/alinescafs3mp-afk/monik_v2/internal/agent/configfile"
	agruntime "github.com/alinescafs3mp-afk/monik_v2/internal/agent/runtime"
	"github.com/alinescafs3mp-afk/monik_v2/internal/agent/setup"
	"github.com/alinescafs3mp-afk/monik_v2/internal/install"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
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
	if st.File.PendingRegistration {
		cred, _ := configfile.ReadCredential(st.File.CredentialPath)
		fmt.Printf("discovery configured; owner approval required. fingerprint=%s\n", protocol.RegistrationFingerprint(st.File.AgentID, cred))
	}
	fmt.Printf("configured agent_id=%s controller=%s config=%s\n", st.File.AgentID, st.File.ControllerURL, filepath.Join(st.File.StateDir, "agent.json"))
	fmt.Println("foreground mode is unmanaged: remote update/restart unavailable until `monik-agent service install`")
	return 0
}

func cmdRun(args []string) int {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	cfg := fs.String("config", setup.DefaultConfigPath(), "absolute config path")
	_ = fs.Parse(args)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := setup.WaitForApproval(ctx, *cfg, os.Stdout); err != nil {
		if ctx.Err() != nil {
			return 0
		}
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	ag, err := agruntime.Open(*cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
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
		opts := install.LinuxDefaults()
		opts.ConfigPath = *cfg
		opts.HostSrc = filepath.Join(filepath.Dir(self), "monik-service-host")
		opts.WorkerSrc = self
		if _, err := os.Stat(opts.HostSrc); err != nil {
			fmt.Fprintln(os.Stderr, "monik-service-host must sit next to monik-agent")
			return 1
		}
		if err := install.InstallLinux(opts); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		st, err := agruntime.Open(*cfg)
		if err == nil {
			st.State.File.Managed = true
			_ = st.State.Save()
		}
		fmt.Println("installed service host into", opts.Prefix, "unit", opts.UnitPath)
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
		opts := install.WindowsDefaults()
		opts.HostSrc = hostBin
		opts.WorkerSrc = self
		opts.ConfigPath = cfg
		if err := install.InstallWindows(opts); err != nil {
			fmt.Fprintln(os.Stderr, err)
			fmt.Println("manual fallback:")
			fmt.Print(servicehost.PlanWindowsService(hostBin, self, cfg))
			return 1
		}
		fmt.Println("installed Windows service MonikAgent")
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
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := setup.WaitForApproval(ctx, *cfg, os.Stdout); err != nil {
		if ctx.Err() != nil {
			return 0
		}
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
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
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "controller show|recover")
		return 2
	}
	fs := flag.NewFlagSet("controller", flag.ExitOnError)
	cfg := fs.String("config", setup.DefaultConfigPath(), "config")
	profile := fs.String("profile", "", "trusted recovery profile")
	_ = fs.Parse(args[1:])
	if len(args) < 1 {
		return 2
	}
	switch args[0] {
	case "show":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if err := setup.WaitForApproval(ctx, *cfg, os.Stdout); err != nil {
			if ctx.Err() != nil {
				return 0
			}
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
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
		fmt.Fprintln(os.Stderr, "automated profile recovery is not implemented; the configuration was NOT changed")
		return 1
	default:
		return 2
	}
}
