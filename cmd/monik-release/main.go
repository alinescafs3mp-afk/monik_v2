package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/alinescafs3mp-afk/monik_v2/internal/tufutil"
	"github.com/alinescafs3mp-afk/monik_v2/internal/version"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, `monik-release %s
  init --keys DIR
  sign --keys DIR --repo DIR --artifact NAME=PATH [--version V] [--notes TEXT]
  verify --repo DIR --target NAME
  pack --repo DIR --output FILE --version V
  renew-metadata --keys DIR --repo DIR
`, version.Version)
		os.Exit(2)
	}
	switch os.Args[1] {
	case "init":
		fs := flag.NewFlagSet("init", flag.ExitOnError)
		dir := fs.String("keys", "release-keys", "offline key directory (never the running server)")
		_ = fs.Parse(os.Args[2:])
		if _, err := tufutil.InitKeys(*dir); err != nil {
			fatal(err)
		}
		fmt.Println("initialized TUF keys in", *dir)
		fmt.Println("keep root and targets keys offline; snapshot/timestamp may be copied to a constrained publisher")
	case "sign":
		fs := flag.NewFlagSet("sign", flag.ExitOnError)
		keys := fs.String("keys", "release-keys", "key dir")
		repo := fs.String("repo", "tuf-repo", "repository dir")
		ver := fs.String("version", version.Version, "release version")
		notes := fs.String("notes", "", "notes")
		_ = fs.Parse(os.Args[2:])
		arts := map[string]string{}
		for _, a := range fs.Args() {
			k, v, ok := splitKV(a)
			if !ok {
				fatal(fmt.Errorf("artifact must be name=path, got %s", a))
			}
			arts[k] = v
		}
		if len(arts) == 0 {
			fatal(fmt.Errorf("at least one name=path artifact required"))
		}
		ks, err := tufutil.InitKeys(*keys)
		if err != nil {
			fatal(err)
		}
		if err := tufutil.SignRepository(ks, *repo, arts, 365, 7); err != nil {
			fatal(err)
		}
		fmt.Println("signed repository", *repo, "version", *ver, "notes", *notes)
	case "verify":
		fs := flag.NewFlagSet("verify", flag.ExitOnError)
		repo := fs.String("repo", "tuf-repo", "repo")
		target := fs.String("target", "", "target path")
		_ = fs.Parse(os.Args[2:])
		if *target == "" {
			fatal(fmt.Errorf("--target required"))
		}
		info, _, err := tufutil.VerifyLocal(*repo, *target)
		if err != nil {
			fatal(err)
		}
		fmt.Printf("verified %s length=%d\n", *target, info.Length)
	case "pack":
		fs := flag.NewFlagSet("pack", flag.ExitOnError)
		repo := fs.String("repo", "tuf-repo", "repo")
		out := fs.String("output", "monik-release.tgz", "bundle")
		ver := fs.String("version", version.Version, "version")
		notes := fs.String("notes", "", "notes")
		_ = fs.Parse(os.Args[2:])
		if err := tufutil.PackBundle(*repo, *out, *ver, *notes); err != nil {
			fatal(err)
		}
		fmt.Println("wrote", *out)
	case "renew-metadata":
		fs := flag.NewFlagSet("renew-metadata", flag.ExitOnError)
		keys := fs.String("keys", "release-keys", "keys")
		repo := fs.String("repo", "tuf-repo", "repo")
		_ = fs.Parse(os.Args[2:])
		ks, err := tufutil.InitKeys(*keys)
		if err != nil {
			fatal(err)
		}
		tgts := map[string]string{}
		_ = filepath.Walk(filepath.Join(*repo, "targets"), func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return err
			}
			rel, _ := filepath.Rel(filepath.Join(*repo, "targets"), path)
			tgts[rel] = path
			return nil
		})
		if err := tufutil.SignRepository(ks, *repo, tgts, 365, 1); err != nil {
			fatal(err)
		}
		fmt.Println("renewed timestamp/snapshot/targets")
	default:
		os.Exit(2)
	}
}

func splitKV(s string) (string, string, bool) {
	for i := 0; i < len(s); i++ {
		if s[i] == '=' {
			return s[:i], s[i+1:], true
		}
	}
	return "", "", false
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
