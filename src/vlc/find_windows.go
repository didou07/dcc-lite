//go:build windows

package vlc

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"golang.org/x/sys/windows/registry"
)

// ErrNotFound means no VLC installation was found.
var ErrNotFound = errors.New("VLC media player was not found.")

func hasLibs(dir string) bool {
	for _, name := range []string{"libvlc.dll", "libvlccore.dll"} {
		if st, err := os.Stat(filepath.Join(dir, name)); err != nil || st.IsDir() {
			return false
		}
	}
	return true
}

func registryDirs() []string {
	var out []string
	for _, view := range []uint32{registry.WOW64_64KEY, registry.WOW64_32KEY} {
		for _, root := range []registry.Key{registry.LOCAL_MACHINE, registry.CURRENT_USER} {
			k, err := registry.OpenKey(root, `SOFTWARE\VideoLAN\VLC`, registry.QUERY_VALUE|view)
			if err != nil {
				continue
			}
			if dir, _, err := k.GetStringValue("InstallDir"); err == nil && dir != "" {
				out = append(out, dir)
			}
			k.Close()
		}
	}
	return out
}

func Candidates() []string {
	var dirs []string
	if env := os.Getenv("DCC_VLC_DIR"); env != "" {
		dirs = append(dirs, env)
	}
	if exe, err := os.Executable(); err == nil {
		dirs = append(dirs, filepath.Dir(exe), filepath.Join(filepath.Dir(exe), "vlc"))
	}
	dirs = append(dirs, registryDirs()...)
	for _, env := range []string{"ProgramW6432", "ProgramFiles", "ProgramFiles(x86)"} {
		if base := os.Getenv(env); base != "" {
			dirs = append(dirs, filepath.Join(base, "VideoLAN", "VLC"))
		}
	}
	return dirs
}

func machineOf(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	head := make([]byte, 4096)
	n, _ := f.Read(head)
	head = head[:n]
	if len(head) < 0x40 || head[0] != 'M' || head[1] != 'Z' {
		return ""
	}
	off := int(binary.LittleEndian.Uint32(head[0x3C:]))
	if off < 0 || off+6 > len(head) || string(head[off:off+4]) != "PE\x00\x00" {
		return ""
	}
	switch binary.LittleEndian.Uint16(head[off+4:]) {
	case 0x8664:
		return "amd64"
	case 0x14c:
		return "386"
	case 0xaa64:
		return "arm64"
	}
	return ""
}

func FindDir() (string, error) {
	var wrongKind string
	seen := map[string]bool{}
	for _, dir := range Candidates() {
		dir = filepath.Clean(dir)
		if seen[dir] || !hasLibs(dir) {
			continue
		}
		seen[dir] = true
		switch m := machineOf(filepath.Join(dir, "libvlc.dll")); m {
		case "", runtime.GOARCH:
			return dir, nil
		default:
			wrongKind = m
		}
	}
	if wrongKind != "" {
		return "", fmt.Errorf("the installed VLC is %s but DCC Lite is %s", wrongKind, runtime.GOARCH)
	}
	return "", ErrNotFound
}
