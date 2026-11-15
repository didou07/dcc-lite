//go:build windows

package updater

import "os"

// swap moves the running executable aside (Windows allows renaming, not
// overwriting, a running program) and puts the new one in its place.
func swap(exe, newExe string) error {
	old := exe + ".old"
	_ = os.Remove(old)
	if err := os.Rename(exe, old); err != nil {
		return err
	}
	if err := os.Rename(newExe, exe); err != nil {
		_ = os.Rename(old, exe)
		return err
	}
	return nil
}
