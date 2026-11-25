//go:build windows

package config

import (
	"fmt"
	"os/exec"
	"os/user"
)

const bestEffortPerms = true

func restrictToOwner(path string, isDir bool) error {
	current, err := user.Current()
	if err != nil {
		return fmt.Errorf("determine current user: %w", err)
	}
	grant := current.Username + ":F"
	if isDir {
		grant = current.Username + ":(OI)(CI)F"
	}
	cmd := exec.Command("icacls", path, "/inheritance:r", "/grant:r", grant)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("restrict permissions on %s: %w: %s", path, err, string(out))
	}
	return nil
}
