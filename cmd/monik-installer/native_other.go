//go:build !linux

package main

import (
	"context"
	"fmt"
	"github.com/alinescafs3mp-afk/monik_v2/internal/installerbundle"
	"io"
)

func runNative(context.Context, *installerbundle.Bundle, io.Writer) error {
	return fmt.Errorf("single-file installation currently supports Linux with systemd only")
}
