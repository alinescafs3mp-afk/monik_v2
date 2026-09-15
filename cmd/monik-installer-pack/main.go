// Build-time utility. It never executes the supplied programs, contacts a server,
// or includes live controller credentials in a reusable template.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/alinescafs3mp-afk/monik_v2/internal/installerbundle"
)

func main() {
	if e := run(); e != nil {
		die(e)
	}
}

func run() error {
	installer := flag.String("installer", "", "static installer stub")
	worker := flag.String("worker", "", "static monik-agent from the same build")
	host := flag.String("supervisor", "", "static monik-service-host from the same build")
	arch := flag.String("arch", "amd64", "Linux architecture")
	build := flag.String("build", "", "source commit")
	out := flag.String("out", "", "output reusable installer template")
	flag.Parse()
	if *installer == "" || *worker == "" || *host == "" || *out == "" || *build == "" {
		return fmt.Errorf("all input paths, --out and --build are required")
	}
	a, e := read(*installer)
	if e != nil {
		return e
	}
	b, e := read(*worker)
	if e != nil {
		return e
	}
	c, e := read(*host)
	if e != nil {
		return e
	}
	// Refuse replacement: publishing a different template is an explicit operator
	// step. A partially written temporary file is never a downloadable template.
	if _, e = os.Lstat(*out); !os.IsNotExist(e) {
		return fmt.Errorf("output exists or cannot be inspected")
	}
	if e = os.MkdirAll(filepath.Dir(*out), 0755); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(*out), ".installer-template-")
	if e != nil {
		return e
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if e = installerbundle.Pack(f, a, b, c, *arch, *build); e == nil {
		e = f.Sync()
	}
	if ce := f.Close(); e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	if e = os.Chmod(tmp, 0644); e != nil {
		return e
	}
	// Link publishes without overwriting a destination created concurrently.
	if e = os.Link(tmp, *out); e != nil {
		return e
	}
	d, e := os.Open(filepath.Dir(*out))
	if e == nil {
		e = d.Sync()
		d.Close()
	}
	if e != nil {
		return e
	}
	fmt.Println("Prepared credential-free installer template:", *out)
	return nil
}
func read(p string) ([]byte, error) {
	f, e := os.Open(p)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	i, e := f.Stat()
	if e != nil {
		return nil, e
	}
	if !i.Mode().IsRegular() || i.Size() > installerbundle.MaxExecutable {
		return nil, fmt.Errorf("invalid executable size/type")
	}
	b, e := io.ReadAll(io.LimitReader(f, installerbundle.MaxExecutable+1))
	if int64(len(b)) > installerbundle.MaxExecutable {
		return nil, fmt.Errorf("executable too large")
	}
	return b, e
}
func die(e error) { fmt.Fprintln(os.Stderr, e); os.Exit(1) }
