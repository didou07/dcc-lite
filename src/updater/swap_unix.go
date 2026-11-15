//go:build !windows

package updater

import "os"

// swap atomically replaces exe with newExe (same directory, same filesystem).
func swap(exe, newExe string) error { return os.Rename(newExe, exe) }
