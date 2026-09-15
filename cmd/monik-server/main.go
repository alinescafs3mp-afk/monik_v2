package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/alinescafs3mp-afk/monik_v2/internal/idgen"
	"github.com/alinescafs3mp-afk/monik_v2/internal/protocol"
	"github.com/alinescafs3mp-afk/monik_v2/internal/secure"
	"github.com/alinescafs3mp-afk/monik_v2/internal/server"
	"github.com/alinescafs3mp-afk/monik_v2/internal/version"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "setup":
		os.Exit(runSetup(os.Args[2:]))
	case "run":
		os.Exit(runServer(os.Args[2:]))
	case "tls-add-name":
		os.Exit(runTLSAddName(os.Args[2:]))
	case "backup":
		os.Exit(runBackup(os.Args[2:]))
	case "restore":
		os.Exit(runRestore(os.Args[2:]))
	case "version", "-version", "--version":
		fmt.Printf("monik-server %s (%s)\n", version.Version, version.Commit)
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `monik-server %s
  setup   complete first-run owner setup
  run     serve UI and agent API
  tls-add-name add a certificate SAN while the controller is stopped
  backup  consistent sqlite snapshot
  restore restore into an empty data directory
`, version.Version)
}

func defaultData() string {
	if d := os.Getenv("MONIK_DATA"); d != "" {
		return d
	}
	if os.Geteuid() == 0 {
		return "/var/lib/monik-server"
	}
	return filepath.Join(os.Getenv("HOME"), ".local", "share", "monik-server")
}

func runSetup(args []string) int {
	fs := flag.NewFlagSet("setup", flag.ExitOnError)
	data := fs.String("data-dir", defaultData(), "data directory")
	listen := fs.String("listen", protocol.DefaultListen, "listen address")
	adv := fs.String("advertised-url", protocol.DefaultBootstrapURL, "advertised agent URL")
	user := fs.String("admin-user", "owner", "administrator username")
	pass := fs.String("admin-password", "", "administrator password (generated if empty)")
	nonint := fs.Bool("non-interactive", false, "do not prompt")
	_ = fs.Parse(args)
	if !*nonint && *pass == "" {
		fmt.Fprintln(os.Stderr, "use --non-interactive --admin-password or pass --admin-password")
		return 2
	}
	if *pass == "" {
		p, err := idgen.Secret(12)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		*pass = p
	}
	app, err := server.Open(server.Config{DataDir: *data, Listen: *listen, AdvertisedURL: *adv, Logger: slog.Default()})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer app.Close()
	if err := app.CompleteSetup(server.SetupRequest{Username: *user, Password: *pass, AdvertisedURL: *adv, Listen: *listen}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	boot := filepath.Join(*data, "admin-bootstrap.txt")
	_ = os.WriteFile(boot, []byte("username="+*user+"\npassword="+*pass+"\nlisten="+*listen+"\nadvertised="+*adv+"\n"), 0o600)
	fmt.Printf("setup complete\n  data: %s\n  listen: %s\n  advertised: %s\n  admin user: %s\n  password file: %s\n", *data, *listen, *adv, *user, boot)
	return 0
}

func runServer(args []string) int {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	data := fs.String("data-dir", defaultData(), "data directory")
	listen := fs.String("listen", "", "override listen address")
	_ = fs.Parse(args)
	app, err := server.Open(server.Config{DataDir: *data, Listen: *listen, Logger: slog.Default()})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer app.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := app.Run(ctx); err != nil && err != context.Canceled {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func runBackup(args []string) int {
	fs := flag.NewFlagSet("backup", flag.ExitOnError)
	data := fs.String("data-dir", defaultData(), "data directory")
	out := fs.String("output", "", "output sqlite file")
	_ = fs.Parse(args)
	if *out == "" {
		fmt.Fprintln(os.Stderr, "--output required")
		return 2
	}
	app, err := server.Open(server.Config{DataDir: *data})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer app.Close()
	if err := app.Store.BackupTo(*out); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	sum, size, _ := secure.SHA256File(*out)
	fmt.Printf("backup %s sha256=%s size=%d\n", *out, sum, size)
	return 0
}

func runRestore(args []string) int {
	fs := flag.NewFlagSet("restore", flag.ExitOnError)
	data := fs.String("data-dir", defaultData(), "empty data directory")
	in := fs.String("input", "", "backup sqlite file")
	_ = fs.Parse(args)
	if *in == "" {
		fmt.Fprintln(os.Stderr, "--input required")
		return 2
	}
	if err := os.MkdirAll(*data, 0o700); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	dst := filepath.Join(*data, "monik.db")
	if _, err := os.Stat(dst); err == nil {
		fmt.Fprintln(os.Stderr, "refusing to restore over existing database")
		return 1
	}
	b, err := os.ReadFile(*in)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := os.WriteFile(dst, b, 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	app, err := server.Open(server.Config{DataDir: *data})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	_ = app.Store.SetSetting("restore_mode", "1")
	app.Close()
	fmt.Println("restored; server will start in restore-mode (disruptive jobs paused)")
	return 0
}
