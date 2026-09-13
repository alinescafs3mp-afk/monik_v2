//go:build !windows

package main

import "github.com/alinescafs3mp-afk/monik_v2/internal/servicehost"

func runWindowsService(h *servicehost.Host) bool { return false }
