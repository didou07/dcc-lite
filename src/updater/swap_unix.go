//go:build !windows

package updater

import "os"

func swap(exe, newExe string) error { return os.Rename(newExe, exe) }
