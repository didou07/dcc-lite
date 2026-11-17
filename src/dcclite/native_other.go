//go:build !(windows && native) && !(linux && native) && !(darwin && native)

package main

// The plain build has no window of its own: the interface opens in the default browser.
const nativeBuild = false

func runNative(nativeDeps) error { return nil }
func nativeClose()               {}
func nativeNotice(string)        {}
