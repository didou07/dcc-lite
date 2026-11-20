//go:build linux && cgo

package vlc

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
)

var libNames = []string{"libvlc.so.5", "libvlc.so"}

var libDirs = func() []string {
	dirs := []string{"/usr/lib", "/usr/lib64", "/usr/local/lib", "/usr/lib/vlc"}
	switch runtime.GOARCH {
	case "amd64":
		dirs = append(dirs, "/usr/lib/x86_64-linux-gnu", "/snap/vlc/current/usr/lib/x86_64-linux-gnu")
	case "arm64":
		dirs = append(dirs, "/usr/lib/aarch64-linux-gnu", "/snap/vlc/current/usr/lib/aarch64-linux-gnu")
	case "arm":
		dirs = append(dirs, "/usr/lib/arm-linux-gnueabihf")
	case "386":
		dirs = append(dirs, "/usr/lib/i386-linux-gnu")
	}
	return dirs
}()

// Candidates lists the libvlc files that exist on this machine.
func Candidates() []string {
	var out []string
	if v := os.Getenv("DCC_LIBVLC"); v != "" {
		if _, err := os.Stat(v); err == nil {
			out = append(out, v)
		}
	}
	for _, d := range libDirs {
		for _, n := range libNames {
			p := filepath.Join(d, n)
			if _, err := os.Stat(p); err == nil {
				out = append(out, p)
			}
		}
	}
	return out
}

func FindLib() (string, error) {
	if c := Candidates(); len(c) > 0 {
		return c[0], nil
	}
	if v := os.Getenv("DCC_LIBVLC"); v != "" {
		return "", errors.New("DCC_LIBVLC does not point to a file: " + v)
	}
	return libNames[0], nil // the loader's own search; LoadFrom explains a failure
}
