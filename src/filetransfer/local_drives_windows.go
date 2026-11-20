//go:build windows

package filetransfer

import (
	"fmt"
	"syscall"
	"unsafe"
)

type drive struct{ Path, Label string }

var (
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	procLogical      = kernel32.NewProc("GetLogicalDrives")
	procDriveType    = kernel32.NewProc("GetDriveTypeW")
	procVolumeInfo   = kernel32.NewProc("GetVolumeInformationW")
	procSetErrorMode = kernel32.NewProc("SetErrorMode")
)

const (
	driveRemovable = 2
	driveFixed     = 3
	driveRemote    = 4
	driveCDROM     = 5
	driveRAMDisk   = 6
)

// listDrives returns the drive letters present, e.g. "(C:) Local Disk" -> "C:/".
func listDrives() []drive {
	// Do not pop up "insert a disk" dialogs for empty card readers.
	previous, _, _ := procSetErrorMode.Call(1)
	defer procSetErrorMode.Call(previous)

	mask, _, _ := procLogical.Call()
	var out []drive
	for i := 0; i < 26; i++ {
		if mask&(1<<uint(i)) == 0 {
			continue
		}
		letter := string(rune('A' + i))
		root := letter + `:\`
		ptr, err := syscall.UTF16PtrFromString(root)
		if err != nil {
			continue
		}
		kind, _, _ := procDriveType.Call(uintptr(unsafe.Pointer(ptr)))
		name := map[uintptr]string{driveRemovable: "Removable Disk", driveFixed: "Local Disk", driveRemote: "Network Drive", driveCDROM: "CD/DVD Drive", driveRAMDisk: "RAM Disk"}[kind]
		if name == "" {
			name = "Disk"
		}
		if kind == driveFixed || kind == driveRemovable {
			var label [261]uint16
			ok, _, _ := procVolumeInfo.Call(uintptr(unsafe.Pointer(ptr)), uintptr(unsafe.Pointer(&label[0])), uintptr(len(label)), 0, 0, 0, 0, 0)
			if ok != 0 {
				if text := syscall.UTF16ToString(label[:]); text != "" {
					name = text
				}
			}
		}
		out = append(out, drive{Path: letter + ":/", Label: fmt.Sprintf("(%s:) %s", letter, name)})
	}
	return out
}
