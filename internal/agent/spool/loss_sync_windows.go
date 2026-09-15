//go:build windows

package spool

// Windows directory handles cannot be synced with os.File.Sync. Native Windows
// crash-durability remains a separate acceptance gate, not a compile-time claim.
func syncLossDirectory(string) error { return nil }
