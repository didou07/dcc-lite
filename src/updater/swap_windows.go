//go:build windows

package updater

import "os"

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
