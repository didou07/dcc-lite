//go:build windows

package crashlog

import (
	"os"
	"syscall"
	"unsafe"
)

func init() {
	h, err := syscall.GetStdHandle(syscall.STD_ERROR_HANDLE)
	if err == nil && h != 0 && h != syscall.InvalidHandle {
		return
	}
	f, err := openStderrFile()
	if err != nil {
		return
	}
	const stdErrorHandle = ^uintptr(11) // STD_ERROR_HANDLE = (DWORD)-12
	set := syscall.NewLazyDLL("kernel32.dll").NewProc("SetStdHandle")
	if r, _, _ := set.Call(stdErrorHandle, f.Fd()); r == 0 {
		f.Close()
		return
	}
	os.Stderr = f // keep f referenced so it is never closed by the finalizer
}

// Notify shows a message box. It is best effort and never panics.
func Notify(title, text string) {
	defer func() { _ = recover() }()
	t, err1 := syscall.UTF16PtrFromString(text)
	c, err2 := syscall.UTF16PtrFromString(title)
	if err1 != nil || err2 != nil {
		return
	}
	proc := syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW")
	proc.Call(0, uintptr(unsafe.Pointer(t)), uintptr(unsafe.Pointer(c)), 0x10) // MB_ICONERROR
}
