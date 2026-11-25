//go:build darwin && cgo

package vlc

import (
	"errors"
	"os"
	"path/filepath"
)

var libNames = []string{"libvlc.dylib"}

var libDirs = func() []string {
	dirs := []string{"/Applications/VLC.app/Contents/MacOS/lib"}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, "Applications", "VLC.app", "Contents", "MacOS", "lib"))
	}
	return dirs
}()

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
	return libNames[0], nil
}
