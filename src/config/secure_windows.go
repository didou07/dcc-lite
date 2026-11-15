//go:build windows

package config

import (
	"fmt"
	"os/exec"
	"os/user"
)

// restrictToOwner limits path to the current user via icacls (os.Chmod does not affect Windows ACLs).
// bestEffortPerms: a failure (FAT/exFAT or network drive) must not stop the app.
const bestEffortPerms = true

func restrictToOwner(path string, isDir bool) error {
	current, err := user.Current()
	if err != nil {
		return fmt.Errorf("determine current user: %w", err)
	}
	grant := current.Username + ":F"
	if isDir {
		grant = current.Username + ":(OI)(CI)F" // folders pass the right on to the files created inside
	}
	cmd := exec.Command("icacls", path, "/inheritance:r", "/grant:r", grant)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("restrict permissions on %s: %w: %s", path, err, string(out))
	}
	return nil
}
