//go:build !windows

package config

import "os"

// bestEffortPerms is false here: a failed chmod is an error.
const bestEffortPerms = false

// restrictToOwner tightens filesystem permissions so only the owning user can
// read or write path. On POSIX systems a plain chmod is sufficient.
func restrictToOwner(path string, isDir bool) error {
	mode := os.FileMode(0o600)
	if isDir {
		mode = 0o700
	}
	return os.Chmod(path, mode)
}
