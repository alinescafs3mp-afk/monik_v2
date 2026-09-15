package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/alinescafs3mp-afk/monik_v2/internal/agentconsole"
	"github.com/alinescafs3mp-afk/monik_v2/internal/install"
	"github.com/alinescafs3mp-afk/monik_v2/internal/servicehost"
	"github.com/alinescafs3mp-afk/monik_v2/internal/version"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "monik-service-host %s\n  run --worker PATH --config PATH --state DIR\n", version.Version)
		os.Exit(2)
	}
	if os.Args[1] == "console-enable" || os.Args[1] == "console-disable" {
		if len(os.Args) != 2 {
			os.Exit(2)
		}
		var err error
		if os.Args[1] == "console-enable" {
			err = install.EnableConsole()
		} else {
			err = install.DisableConsole()
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println("Local console policy applied. Check the live channel in Monik; monitoring identity unchanged.")
		return
	}
	if os.Args[1] == "version" {
		fmt.Println(version.Version)
		return
	}
	if os.Args[1] == "console-session" {
		fs := flag.NewFlagSet("console-session", flag.ExitOnError)
		uid := fs.Int("agent-uid", -1, "authorized local monitoring UID")
		_ = fs.Parse(os.Args[2:])
		if fs.NArg() != 0 {
			os.Exit(2)
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if err := agentconsole.ServeActivated(ctx, *uid); err != nil {
			fmt.Fprintln(os.Stderr, "console session rejected:", err)
			os.Exit(1)
		}
		return
	}
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	worker := fs.String("worker", "", "worker executable")
	cfg := fs.String("config", "", "worker config")
	state := fs.String("state", "", "state directory")
	_ = fs.Parse(os.Args[2:])
	if *worker == "" {
		self, _ := os.Executable()
		*worker = filepath.Join(filepath.Dir(self), "monik-agent")
	}
	if *state == "" {
		*state = filepath.Dir(*cfg)
	}
	self, _ := os.Executable()
	h := &servicehost.Host{WorkerBin: *worker, WorkerCfg: *cfg, StateDir: *state, SelfBin: self}
	if runWindowsService(h) {
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := h.Run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
