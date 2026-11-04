//go:build !windows

package main

import "os/exec"

func startDetached(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	return cmd.Start()
}
