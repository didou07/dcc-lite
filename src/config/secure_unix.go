//go:build !windows

package config

import "os"

const bestEffortPerms = false

func restrictToOwner(path string, isDir bool) error {
	mode := os.FileMode(0o600)
	if isDir {
		mode = 0o700
	}
	return os.Chmod(path, mode)
}
