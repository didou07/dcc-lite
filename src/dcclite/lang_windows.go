//go:build windows

package main

import "golang.org/x/sys/windows"

func osLang() string {
	r, _, _ := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetUserDefaultUILanguage").Call()
	return map[uintptr]string{0x01: "ar", 0x07: "de", 0x0a: "es", 0x0c: "fr", 0x19: "ru", 0x1f: "tr", 0x04: "zh"}[r&0x3ff]
}
