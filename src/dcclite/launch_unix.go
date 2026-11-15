//go:build !windows

package main

import (
	"os"
	"os/exec"
	"syscall"
)

func startDetached(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	return cmd.Start()
}

// restartSelf replaces this process with the (updated) executable, keeping the PID.
func restartSelf(exe string, args []string) error {
	return syscall.Exec(exe, append([]string{exe}, args...), os.Environ())
}
