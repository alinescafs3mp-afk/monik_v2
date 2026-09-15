package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/alinescafs3mp-afk/monik_v2/internal/processlock"
	"github.com/alinescafs3mp-afk/monik_v2/internal/tlsutil"
)

func runTLSAddName(args []string) int {
	fs := flag.NewFlagSet("tls-add-name", flag.ContinueOnError)
	data := fs.String("data-dir", defaultData(), "existing controller data directory")
	name := fs.String("name", "", "additional IP/DNS SAN (not a URL)")
	if e := fs.Parse(args); e != nil {
		return 2
	}
	if *name == "" || fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "--name is required")
		return 2
	}
	unlock, err := processlock.Acquire(filepath.Join(*data, "controller.lock"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "Stop the controller first:", err)
		return 1
	}
	defer unlock()
	dir := filepath.Join(*data, "tls")
	// This command must never bootstrap a replacement CA on an incomplete copy.
	for _, f := range []string{"ca.crt", "ca.key"} {
		if info, e := os.Lstat(filepath.Join(dir, f)); e != nil || !info.Mode().IsRegular() {
			fmt.Fprintln(os.Stderr, "Existing regular CA files required; restore original TLS state")
			return 1
		}
	}
	if _, e := os.Stat(filepath.Join(dir, "leaf.bundle.pem")); os.IsNotExist(e) {
		for _, f := range []string{"leaf.crt", "leaf.key"} {
			if _, e := os.Stat(filepath.Join(dir, f)); e != nil {
				fmt.Fprintln(os.Stderr, "Existing leaf pair required")
				return 1
			}
		}
	} else if e != nil {
		fmt.Fprintln(os.Stderr, e)
		return 1
	}
	b, err := tlsutil.LoadOrCreate(dir, nil, nil, 90*24*time.Hour)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	changed, err := b.AddServerName(dir, *name)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("TLS name %s ready (certificate changed=%v). CA, controller identity, advertised URL and agent routes unchanged. Start the controller and re-check the profile.\n", *name, changed)
	return 0
}
