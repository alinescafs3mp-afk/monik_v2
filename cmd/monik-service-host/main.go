package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/alinescafs3mp-afk/monik_v2/internal/servicehost"
	"github.com/alinescafs3mp-afk/monik_v2/internal/version"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "monik-service-host %s\n  run --worker PATH --config PATH --state DIR\n", version.Version)
		os.Exit(2)
	}
	if os.Args[1] == "version" {
		fmt.Println(version.Version)
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
