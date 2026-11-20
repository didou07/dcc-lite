//go:build !windows

package filetransfer

import (
	"os"
	"os/user"
	"path/filepath"
	"sort"
)

type drive struct{ Path, Label string }

func listDrives() []drive {
	out := []drive{{Path: "/", Label: "/ (root)"}}
	userName := ""
	if current, err := user.Current(); err == nil {
		userName = current.Username
	}
	for _, base := range []string{"/mnt", "/media", "/run/media", "/Volumes"} {
		entries, err := os.ReadDir(base)
		if err != nil {
			continue
		}
		var found []drive
		for _, entry := range entries {
			full := filepath.Join(base, entry.Name())
			info, err := os.Stat(full)
			if err != nil || !info.IsDir() {
				continue
			}
			// /media/<user>/<disk>: show the disks, not the user folder.
			if entry.Name() == userName && userName != "" && (base == "/media" || base == "/run/media") {
				if sub, err := os.ReadDir(full); err == nil {
					for _, child := range sub {
						childPath := filepath.Join(full, child.Name())
						if ci, err := os.Stat(childPath); err == nil && ci.IsDir() {
							found = append(found, drive{Path: childPath, Label: childPath})
						}
					}
				}
				continue
			}
			found = append(found, drive{Path: full, Label: full})
		}
		sort.Slice(found, func(i, j int) bool { return found[i].Path < found[j].Path })
		out = append(out, found...)
	}
	return out
}
