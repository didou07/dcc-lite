//go:build windows

package config

import (
	"fmt"
	"os/exec"
	"os/user"
)

// restrictToOwner tightens filesystem permissions so only the current user
// can read or write path. os.Chmod has no effect on Windows ACLs, so this
// resets inheritance and grants full control to the current user only via
// icacls, which ships with every supported Windows release.
// bestEffortPerms: on Windows a failure (a FAT/exFAT or network drive has no ACLs) must not stop the app.
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
